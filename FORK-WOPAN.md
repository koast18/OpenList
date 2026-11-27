# OpenList fork：联通云盘（WoPan）驱动兼容改造

这是 `OpenListTeam/OpenList` 的 fork，目的只有一个：让 `drivers/wopan` 能吃下从**联通云盘 Android 客户端**（`com.chinaunicom.bol.cloudapp`）拿到的凭据，而不是只认网页版那一套。

上游行为（`7c26f10` 及之前）能用，但有三类硬伤，逆向结论见 `../analysis/11_凭据审计_APK登录_vs_OpenList协议.md`。

## 改了什么

### 1. 客户端身份可配（核心）

`wopan-sdk-go` 原来把 `clientId=1001000021` / `clientSecret`（网页版）写死在包级常量里。但协议上：

- `clientId` 在**每个加密 body 里**；
- `api-user` 通道的 **AES key 就是 clientSecret**。

所以「App 签发的 token + 网页版的 secret 续期」在协议层就是错的，表现为续期 8005 / 1001，用户以为 token 过期。

现在：

| 常量 | 值 | 用途 |
|---|---|---|
| `ClientIDAndroid` / `ClientSecretAndroid` | `1001000035` / `iELf0UL07o6I8eRK` | Android App |
| `ClientIDPCWeb` / `ClientSecretPCWeb`（= `Default*`） | `1001000021` / `XFmi9GS2hzk98jGX` | 网页版，保持老行为 |

驱动表单新增 `client_mark`（`auto`/`android`/`pc_web`）+ 高级覆盖 `client_id`/`client_secret`；`auto` 会按登录方式选一套，**失败自动换另一套重试并记住**。

### 2. 直接用手机号登录

新增 `phone` / `password` / `sms_code` / `verify_uuid` / `verify_code`，驱动通过 `AppLoginAccount`（或 `AppLoginByMobile` / 双因素 `AppLoginDoubleAuth`）自己换 token，不再是"必须先去抓一个 token"。图形验证码按报错提示取图回填。

### 3. 空间与能力

- `space` 下拉 13 种（个人/家庭/私密/微信备份/相册备份/沃相机/沃铃/沃视频/沃邮箱/沃报告/账单/OCR/媒体广场）
- `private_space_password` → 自动换 `psToken`
- `family_id` 留空则用账号默认家庭；**无家庭组不再导致挂载失败**
- `sort_rule` 真正下传（服务端只有 time_desc/name_asc/name_desc/time_asc，没有按大小）

### 4. 顺手修的 bug

空下载列表 panic、个人空间建目录混进 `familyId`、上传零重试、`ownerId` json tag 多两个空格、`vipLevel` 恒 `0`、`1001` 不触发刷新、包名还叫 `template`。

## 构建

**只用 GitHub Actions**，不在本地/远端编译：

```bash
gh workflow run fork-image.yml --repo koast18/OpenList -f tag=wopan
```

工作流 `.github/workflows/fork-image.yml`：拉前端 dist → `go mod tidy` → `go vet` + `go test ./drivers/wopan/` → `go build` → 用 `Dockerfile.fork`（官方 base 镜像）打成 `ghcr.io/koast18/openlist-wopan:wopan`。

## 依赖

```go
replace github.com/OpenListTeam/wopan-sdk-go => github.com/koast18/wopan-sdk-go v0.3.2
```

## 验证状态

- ✅ CI：`go vet` + 3 个单测（身份回退 / 内置登录 / space+sort 映射）全绿
- ✅ 部署后实测：无效 token 建存储 → 报错里出现 `(also tried the android client identity: ...)`，证明回退路径在生产二进制里真的执行
- ❌ 没有真实联通账号验证登录/列表/上传
