package wopandriver

import (
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
)

// Addition is the storage form.
//
// Two credential shapes are supported and both are normalised into a
// wopan token pair at mount time:
//
//  1. tokens obtained elsewhere (refresh_token and/or access_token);
//  2. phone + password, which the driver exchanges through the Android
//     client's AppLoginAccount, replicating the app exactly.
//
// client_mark selects the client identity that mints/refreshes the tokens. It
// is not cosmetic: the api-user channel encrypts with the client secret and
// every body carries the clientId, so a token minted by the Android app cannot
// be refreshed by the PC/Web pair. Empty means "derive it": the Android pair for
// the built-in login, the PC/Web pair for a pasted token (the historical
// behaviour), with an automatic fallback in either direction.
type Addition struct {
	driver.RootID

	// --- credentials ---
	RefreshToken string `json:"refresh_token" type:"text" help:"refresh token; either this or access_token, or phone+password"`
	AccessToken  string `json:"access_token" type:"text" help:"optional access token (>=16 chars); refreshed automatically when missing or too short"`
	Phone        string `json:"phone" type:"text" help:"手机号：与 password 一起填即可自动登录换取 token"`
	Password     string `json:"password" type:"text" help:"登录密码（AppLoginAccount）"`
	SmsCode      string `json:"sms_code" type:"text" help:"短信验证码：与 phone 一起填则走 AppLoginByMobile（填了 password 则走双重认证）。验证码请点下面的「发送短信验证码」按钮获取"`
	VerifyUUID   string `json:"verify_uuid" type:"text" help:"图形验证码 uuid，与 verify_code 必须成对填；见挂载报错里的链接"`
	VerifyCode   string `json:"verify_code" type:"text" help:"图形验证码字符"`
	ClientMark   string `json:"client_mark" type:"select" options:"auto,android,pc_web" default:"auto" help:"token 的签发方：android=联通云盘 App(1001000035)，pc_web=网页版(1001000021)，auto=按登录方式选并自动回退"`
	ClientID     string `json:"client_id" type:"text" help:"高级：覆盖 clientId"`
	ClientSecret string `json:"client_secret" type:"text" help:"高级：覆盖 clientSecret（同时是 api-user 通道的 AES key）"`

	// --- space ---
	Space      string `json:"space" type:"select" options:"personal,family,private,wechat_backup,album_backup,wo_camera,wo_ring,wo_video,wo_mail,wo_report,bill,ocr,media_square" default:"personal" help:"挂载哪个空间；family 需要 family_id，private 需要私密空间密码或 ps_token"`
	FamilyID   string `json:"family_id" type:"text" help:"家庭空间 id；留空则用账号的默认家庭"`
	PrivatePwd string `json:"private_space_password" type:"text" help:"私密空间密码（用于换 psToken）"`
	PsToken    string `json:"ps_token" type:"text" help:"已有 psToken 时可直接填；否则用 private_space_password 自动换"`

	SortRule string `json:"sort_rule" type:"select" options:"time_desc,name_asc,name_desc,time_asc" default:"time_desc" help:"服务端排序（无按大小排序）"`
}

// DriverName is the name the driver registers under; it is also what the
// storage row stores in its `driver` column.
const DriverName = "WoPan"

var config = driver.Config{
	Name:              DriverName,
	DefaultRoot:       "0",
	NoOverwriteUpload: true,
}

func init() {
	op.RegisterDriver(func() driver.Driver {
		return &Wopan{}
	})
}
