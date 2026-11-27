package handles

import (
	"context"
	"encoding/json"
	"strconv"

	wopandriver "github.com/OpenListTeam/OpenList/v4/drivers/wopan"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
)

// SmsSender is implemented by drivers that expose an explicit "send one SMS code"
// action. It exists because the storage form has no button control and OpenList
// re-initialises a storage on every load, so a code request triggered by saving
// would fire repeatedly.
type SmsSender interface {
	SendSmsCode(ctx context.Context) error
}

// SendStorageSmsCode sends exactly one SMS code for a storage, on an explicit
// user action (the button injected by the wopan frontend snippet).
//
// The storage is read back from the database and the driver is rebuilt from its
// saved addition, so the request always uses the phone number and client
// identity currently stored, never a half-edited form.
func SendStorageSmsCode(c *gin.Context) {
	id, err := strconv.ParseUint(c.Query("id"), 10, 32)
	if err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	storage, err := db.GetStorageById(uint(id))
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	if storage.Driver != wopandriver.DriverName {
		common.ErrorStrResp(c, "this storage does not support sending an SMS code", 400)
		return
	}
	newDriver, err := op.GetDriver(storage.Driver)
	if err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	instance := newDriver()
	instance.SetStorage(*storage)
	if err := json.Unmarshal([]byte(storage.Addition), instance.GetAddition()); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	sender, ok := instance.(SmsSender)
	if !ok {
		common.ErrorStrResp(c, "this driver has no SMS code action", 400)
		return
	}
	if err := sender.SendSmsCode(c.Request.Context()); err != nil {
		common.ErrorResp(c, err, 500)
		return
	}
	common.SuccessResp(c, nil)
}
