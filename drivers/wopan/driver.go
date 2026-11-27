package wopandriver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	wopansdk "github.com/OpenListTeam/wopan-sdk-go"
	"github.com/go-resty/resty/v2"
)

// minAccessTokenLen mirrors the check inside the SDK: the first 16 bytes of the
// access token are used as the AES key of every dispatcher request, so a token
// shorter than that can never be used and must be refreshed first.
const minAccessTokenLen = 16

// upload2CRetryTimes is how many times a single part is retried by the SDK.
// Without it a single flaky 8 MiB part fails the whole upload.
const upload2CRetryTimes = 3

type Wopan struct {
	model.Storage
	Addition
	client          *wopansdk.WoClient
	defaultFamilyID string
	vipLevel        string
	// clientMarkResolved is the client identity that actually worked, so the
	// auto-detection only costs one extra request per mount.
	clientMarkResolved string
}

// onTokenRefreshed keeps the driver storage in sync with the tokens issued by
// the SDK, so a refresh survives restarts.
func (d *Wopan) onTokenRefreshed(accessToken, refreshToken string) {
	d.setTokens(accessToken, refreshToken)
	op.MustSaveDriverStorage(d)
}

func (d *Wopan) setTokens(accessToken, refreshToken string) {
	if accessToken != "" {
		d.AccessToken = accessToken
	}
	if refreshToken != "" {
		d.RefreshToken = refreshToken
	}
}

func (d *Wopan) Config() driver.Config {
	return config
}

func (d *Wopan) GetAddition() driver.Additional {
	return &d.Addition
}

func (d *Wopan) Init(ctx context.Context) error {
	d.client = wopansdk.DefaultWithRefreshToken(d.RefreshToken)
	d.client.SetAccessToken(d.AccessToken)
	d.client.SetPhone(d.Phone)
	d.client.OnRefreshToken(d.onTokenRefreshed)
	d.applyClientCredentials(d.preferredClientMark())
	return d.initClient(ctx)
}

// preferredClientMark decides which client identity to try first.
//
//	explicit android/pc_web  -> that one
//	built-in login (phone)   -> android, because the login itself is the app's
//	pasted token             -> pc_web, the SDK's historical default
func (d *Wopan) preferredClientMark() string {
	switch d.ClientMark {
	case "android", "pc_web":
		return d.ClientMark
	}
	if d.usesBuiltinLogin() {
		return "android"
	}
	return "pc_web"
}

func (d *Wopan) usesBuiltinLogin() bool {
	return d.Phone != "" && (d.Password != "" || d.SmsCode != "")
}

// SendSmsCode is the manual "send me one SMS code" action behind the
// /api/admin/storage/wopan/send_sms_code endpoint.
//
// It exists because OpenList has no button control in the storage form and
// re-initialises a storage on every load: making the code request a side effect
// of saving turned a broken login into a stream of text messages. The frontend
// snippet in wopan_form.js calls this on an explicit click only.
func (d *Wopan) SendSmsCode(ctx context.Context) error {
	client := wopansdk.DefaultWithRefreshToken(d.RefreshToken)
	client.SetAccessToken(d.AccessToken)
	client.SetPhone(d.Phone)
	client.SetClientCredentials(d.resolveCredentials())
	// Same reason as in initClient: without the browser UA the edge answers 403.
	client.SetUserAgent(wopansdk.DefaultUA)
	if d.Phone == "" {
		return errors.New("请先填写手机号（phone）")
	}
	if err := client.SendMessageCode(d.Phone, 1, "app_send", func(req *resty.Request) {
		req.SetContext(ctx)
	}); err != nil {
		return fmt.Errorf("发送短信验证码失败：%w", err)
	}
	return nil
}

// resolveCredentials returns the client identity the storage is configured with,
// following the same resolution order Init uses.
func (d *Wopan) resolveCredentials() (string, string) {
	mark := d.preferredClientMark()
	clientID, clientSecret := wopansdk.DefaultClientID, wopansdk.DefaultClientSecret
	switch mark {
	case "android":
		clientID, clientSecret = wopansdk.ClientIDAndroid, wopansdk.ClientSecretAndroid
	case "pc_web":
		clientID, clientSecret = wopansdk.ClientIDPCWeb, wopansdk.ClientSecretPCWeb
	}
	if d.ClientID != "" {
		clientID = d.ClientID
	}
	if d.ClientSecret != "" {
		clientSecret = d.ClientSecret
	}
	return clientID, clientSecret
}

// applyClientCredentials installs the credential pair, honouring the explicit
// overrides first.
func (d *Wopan) applyClientCredentials(mark string) {
	clientID, clientSecret := wopansdk.DefaultClientID, wopansdk.DefaultClientSecret
	switch mark {
	case "android":
		clientID, clientSecret = wopansdk.ClientIDAndroid, wopansdk.ClientSecretAndroid
	case "pc_web":
		clientID, clientSecret = wopansdk.ClientIDPCWeb, wopansdk.ClientSecretPCWeb
	}
	if d.ClientID != "" {
		clientID = d.ClientID
	}
	if d.ClientSecret != "" {
		clientSecret = d.ClientSecret
	}
	d.client.SetClientCredentials(clientID, clientSecret)
	d.clientMarkResolved = mark
}
func alternateClientMark(mark string) string {
	if mark == "android" {
		return "pc_web"
	}
	return "android"
}

// initClient prepares the client before any dispatcher request is served.
//
// Three things happen here, in this order:
//
//  1. The browser User-Agent must be applied to the underlying resty client.
//     The SDK only records it in WithUA/SetUA (and uses w.ua for upload2C), so
//     dispatcher requests would otherwise be sent with resty's default
//     "go-resty/..." UA, which the WoPan edge rejects with HTTP 403.
//  2. A usable token pair must exist: from the form, by refreshing, or by
//     logging in with phone+password (AppLoginAccount) / phone+sms code.
//  3. Optional data (family id, private-space psToken) is resolved.
func (d *Wopan) initClient(ctx context.Context) error {
	d.client.SetUserAgent(wopansdk.DefaultUA)

	if err := d.ensureTokens(ctx); err != nil {
		return err
	}
	if err := d.client.InitData(); err != nil {
		return fmt.Errorf("initialize user data: %w", err)
	}

	// The default family id is only an optimisation: an account without any
	// family group answers 120002/10002/10004 here, and that must not stop the
	// personal space from mounting.
	if fml, err := d.client.FamilyUserCurrentEncode(func(req *resty.Request) {
		req.SetContext(ctx)
	}); err == nil {
		d.defaultFamilyID = strconv.Itoa(fml.DefaultHomeId)
	}

	return d.initPrivateSpace(ctx)
}

// ensureTokens makes sure a usable access token is in memory.
func (d *Wopan) ensureTokens(ctx context.Context) error {
	if len(d.AccessToken) >= minAccessTokenLen {
		return nil
	}

	switch {
	case d.RefreshToken != "":
		if err := d.refreshWithFallback(ctx); err != nil {
			return fmt.Errorf("refresh access token: %w", err)
		}
	case d.usesBuiltinLogin():
		if err := d.login(ctx); err != nil {
			return err
		}
	default:
		return fmt.Errorf("an access token of at least %d characters, a refresh token, or phone+password is required", minAccessTokenLen)
	}

	if accessToken, _ := d.client.GetToken(); len(accessToken) < minAccessTokenLen {
		return errors.New("the server returned an invalid access token")
	}
	d.setTokens(d.client.GetToken())
	return nil
}

// refreshWithFallback refreshes the token, retrying once with the other client
// identity when the configured one is rejected. That is what makes a token
// pasted from the Android app work without the user knowing about client ids.
func (d *Wopan) refreshWithFallback(ctx context.Context) error {
	err := d.client.RefreshToken()
	if err == nil {
		return nil
	}
	if d.ClientID != "" && d.ClientSecret != "" {
		return err // the user pinned the pair explicitly, do not second-guess it
	}
	firstErr := err
	alt := alternateClientMark(d.clientMarkResolved)
	d.applyClientCredentials(alt)
	if err := d.client.RefreshToken(); err != nil {
		// Restore the pair the user asked for so the error message matches the
		// configuration they see in the form.
		d.applyClientCredentials(alternateClientMark(alt))
		return fmt.Errorf("%w (also tried the %s client identity: %v)", firstErr, alt, err)
	}
	// The fallback worked: keep it. The tokens are already persisted by the
	// OnRefreshToken callback, which the SDK invokes after every refresh.
	return nil
}

// login exchanges the configured phone number for a token pair, replicating the
// Android app's AppLoginAccount / AppLoginByMobile.
//
// Nothing in here sends an SMS. Some accounts cannot log in with a password
// alone: the server answers {"needSmsCode":"1"} with no token. Getting a code is
// a separate, explicit action (SendSmsCode, wired to a button in the storage
// form), because OpenList re-initialises a storage on every load and an
// automatic request would spam the owner.
func (d *Wopan) login(ctx context.Context) error {
	opt := func(req *resty.Request) { req.SetContext(ctx) }
	clientID := d.client.ClientID()

	if d.SmsCode != "" {
		if _, err := d.client.AppLoginByMobile(d.Phone, d.SmsCode, d.Password, opt); err != nil {
			return d.loginError(err, clientID)
		}
		return nil
	}

	_, err := d.client.AppLoginAccount(d.Phone, d.Password, d.VerifyUUID, d.VerifyCode, opt)
	if err == nil {
		return nil
	}
	if errors.Is(err, wopansdk.ErrSmsCodeRequired) {
		return d.loginError(err, clientID)
	}
	// A rejected login can also mean "wrong client identity": retry once with the
	// other pair before giving up.
	if d.ClientID == "" && d.ClientSecret == "" {
		alt := alternateClientMark(d.clientMarkResolved)
		d.applyClientCredentials(alt)
		_, altErr := d.client.AppLoginAccount(d.Phone, d.Password, d.VerifyUUID, d.VerifyCode, opt)
		if altErr == nil {
			return nil
		}
		d.applyClientCredentials(alternateClientMark(alt))
		err = altErr
	}
	return d.loginError(err, clientID)
}

// loginError turns the SDK's signals into something a storage-form user can act
// on: the SMS-code gate, the captcha link, and the credential-pair hint.
func (d *Wopan) loginError(err error, clientID string) error {
	switch {
	case errors.Is(err, wopansdk.ErrSmsCodeRequired):
		return fmt.Errorf("这个账号不能只用密码登录，服务端要求短信验证码（clientId=%s）。"+
			"本驱动不会自动发短信：请在存储编辑页点「发送短信验证码」按钮（每次点击只发一条），"+
			"收到后填进 sms_code 再保存", clientID)
	case errors.Is(err, wopansdk.ErrVerificationCodeRequired):
		return fmt.Errorf("联通云盘要求图形验证码：先用任意 uuid 打开 %s/api-user/getverifycode?uuid=<uuid> 看图"+
			"（把 <uuid> 换成你自己生成的随机串），再把该串填进 verify_uuid、图上字符填进 verify_code，然后重新挂载。"+
			"当前 clientId=%s", wopansdk.DefaultBaseURL, clientID)
	}
	return fmt.Errorf("login failed: %w (clientId=%s; if the password is correct, try switching client_mark)", err, clientID)
}

// initPrivateSpace resolves the psToken of the private space, which is not part
// of any token pair: it comes from a separate password login.
func (d *Wopan) initPrivateSpace(ctx context.Context) error {
	spaceType := d.getSpaceType()
	if spaceType != wopansdk.SpaceTypePrivate {
		return nil
	}
	if d.PsToken != "" {
		d.client.SetPsToken(d.PsToken)
		return nil
	}
	if d.PrivatePwd == "" {
		return errors.New("the private space needs private_space_password or ps_token")
	}
	return d.client.PrivateSpaceLogin(d.PrivatePwd, func(req *resty.Request) {
		req.SetContext(ctx)
	})
}

func (d *Wopan) Drop(ctx context.Context) error {
	return nil
}

func (d *Wopan) List(ctx context.Context, dir model.Obj, args model.ListArgs) ([]model.Obj, error) {
	var res []model.Obj
	// The server pages from 0 (the app also sends pageNum "0" for the first
	// page) and sortRule has to be forwarded or the sort_rule option does
	// nothing at all.
	pageNum := 0
	pageSize := 100
	sortRule := d.getSortRule()
	for {
		data, err := d.client.QueryAllFiles(d.getSpaceType(), dir.GetID(), pageNum, pageSize, sortRule, d.getFamilyID(), func(req *resty.Request) {
			req.SetContext(ctx)
		})
		if err != nil {
			return nil, err
		}
		objs, err := utils.SliceConvert(data.Files, fileToObj)
		if err != nil {
			return nil, err
		}
		res = append(res, objs...)
		if len(data.Files) < pageSize {
			break
		}
		pageNum++
	}
	return res, nil
}

func (d *Wopan) Link(ctx context.Context, file model.Obj, args model.LinkArgs) (*model.Link, error) {
	f, ok := file.(*Object)
	if !ok {
		return nil, fmt.Errorf("unable to convert file to Object")
	}
	res, err := d.client.GetDownloadUrlV2([]string{f.FID}, func(req *resty.Request) {
		req.SetContext(ctx)
	})
	if err != nil {
		return nil, err
	}
	if len(res.List) == 0 {
		return nil, errors.New("empty download url list")
	}
	entry := res.List[0]
	// entry.M3u8 (HLS playlist) and entry.VideoDuration are decoded by the SDK as
	// well; the download URL stays the default here because Link is also used for
	// saving the original file, and m3u8 is a transcoded stream.
	return &model.Link{URL: entry.DownloadUrl}, nil
}

func (d *Wopan) MakeDir(ctx context.Context, parentDir model.Obj, dirName string) error {
	// CreateDirectory only takes familyId when the target really is a family
	// space; the app omits the field entirely otherwise
	// (CreateDirectoryRequest.bodyObject checks isEmpty first). Passing the
	// default family id together with spaceType=0 asks the server to create a
	// folder in the family space the caller did not select.
	spaceType := d.getSpaceType()
	familyID := ""
	if spaceType == wopansdk.SpaceTypeFamily {
		familyID = d.getFamilyID()
	}
	_, err := d.client.CreateDirectory(spaceType, parentDir.GetID(), dirName, familyID, func(req *resty.Request) {
		req.SetContext(ctx)
	})
	return err
}

func (d *Wopan) Move(ctx context.Context, srcObj, dstDir model.Obj) error {
	dirList := make([]string, 0)
	fileList := make([]string, 0)
	if srcObj.IsDir() {
		dirList = append(dirList, srcObj.GetID())
	} else {
		fileList = append(fileList, srcObj.GetID())
	}
	return d.client.MoveFile(dirList, fileList, dstDir.GetID(),
		d.getSpaceType(), d.getSpaceType(),
		d.getFamilyID(), d.getFamilyID(), func(req *resty.Request) {
			req.SetContext(ctx)
		})
}

func (d *Wopan) Rename(ctx context.Context, srcObj model.Obj, newName string) error {
	_type := 1
	if srcObj.IsDir() {
		_type = 0
	}
	return d.client.RenameFileOrDirectory(d.getSpaceType(), _type, srcObj.GetID(), newName, d.getFamilyID(), func(req *resty.Request) {
		req.SetContext(ctx)
	})
}

func (d *Wopan) Copy(ctx context.Context, srcObj, dstDir model.Obj) error {
	dirList := make([]string, 0)
	fileList := make([]string, 0)
	if srcObj.IsDir() {
		dirList = append(dirList, srcObj.GetID())
	} else {
		fileList = append(fileList, srcObj.GetID())
	}
	return d.client.CopyFile(dirList, fileList, dstDir.GetID(),
		d.getSpaceType(), d.getSpaceType(),
		d.getFamilyID(), d.getFamilyID(), func(req *resty.Request) {
			req.SetContext(ctx)
		})
}

func (d *Wopan) Remove(ctx context.Context, obj model.Obj) error {
	dirList := make([]string, 0)
	fileList := make([]string, 0)
	if obj.IsDir() {
		dirList = append(dirList, obj.GetID())
	} else {
		fileList = append(fileList, obj.GetID())
	}
	return d.client.DeleteFile(d.getSpaceType(), d.getVipLevel(ctx), dirList, fileList, func(req *resty.Request) {
		req.SetContext(ctx)
	})
}

func (d *Wopan) Put(ctx context.Context, dstDir model.Obj, stream model.FileStreamer, up driver.UpdateProgress) error {
	// Prefer the seekable view of the stream: upload2C retries a part by
	// rewinding, which only works when the source implements io.Seeker.
	var src io.Reader = stream
	if f := stream.GetFile(); f != nil {
		src = f
	}
	_, err := d.client.Upload2C(d.getSpaceType(), wopansdk.Upload2CFile{
		Name:        stream.GetName(),
		Size:        stream.GetSize(),
		Content:     driver.NewLimitedUploadStream(ctx, src),
		ContentType: stream.GetMimetype(),
	}, dstDir.GetID(), d.getFamilyID(), wopansdk.Upload2COption{
		OnProgress: func(current, total int64) {
			up(100 * float64(current) / float64(total))
		},
		Ctx:        ctx,
		RetryTimes: upload2CRetryTimes,
	})
	return err
}

func (d *Wopan) GetDetails(ctx context.Context) (*model.StorageDetails, error) {
	quota, err := d.client.QueryCloudUsageInfo()
	if err != nil {
		return nil, err
	}
	total, err := strconv.ParseInt(quota.UsageInfo.ByteTotalSize, 10, 64)
	if err != nil {
		return nil, err
	}
	// QueryCloudUsageInfo already carries the membership level; keep it so
	// Remove does not have to fetch it again.
	if quota.VipLevel != "" {
		d.vipLevel = quota.VipLevel
	}
	return &model.StorageDetails{
		DiskUsage: model.DiskUsage{
			TotalSpace: total,
			UsedSpace:  quota.UsageInfo.ByteUsedSize,
		},
	}, nil
}

var _ driver.Driver = (*Wopan)(nil)
var _ driver.WithDetails = (*Wopan)(nil)
