package wopandriver

import (
	"context"
	"time"

	wopansdk "github.com/OpenListTeam/wopan-sdk-go"
	"github.com/go-resty/resty/v2"
)

// do others that not defined in Driver interface

// getSpaceType maps the space option onto the wire spaceType.
//
// The empty value keeps the historical behaviour (personal unless a family id
// was configured) so existing storages keep working untouched.
func (d *Wopan) getSpaceType() string {
	switch d.Space {
	case "personal":
		return wopansdk.SpaceTypePersonal
	case "family":
		return wopansdk.SpaceTypeFamily
	case "private":
		return wopansdk.SpaceTypePrivate
	case "wechat_backup":
		return wopansdk.SpaceTypeWechatBack
	case "album_backup":
		return wopansdk.SpaceTypeAlbumBackup
	case "wo_camera":
		return wopansdk.SpaceTypeWoCamera
	case "wo_ring":
		return wopansdk.SpaceTypeWoRing
	case "wo_video":
		return wopansdk.SpaceTypeWoVideo
	case "wo_mail":
		return wopansdk.SpaceTypeWoMail
	case "wo_report":
		return wopansdk.SpaceTypeWoReport
	case "bill":
		return wopansdk.SpaceTypeBill
	case "ocr":
		return wopansdk.SpaceTypeOCR
	case "media_square":
		return wopansdk.SpaceTypeMediaSquare
	}
	if d.FamilyID != "" {
		return wopansdk.SpaceTypeFamily
	}
	return wopansdk.SpaceTypePersonal
}

// getFamilyID returns the family group id to send along with spaceType=1.
func (d *Wopan) getFamilyID() string {
	if d.FamilyID != "" {
		return d.FamilyID
	}
	if d.getSpaceType() == wopansdk.SpaceTypeFamily {
		return d.defaultFamilyID
	}
	return ""
}

// getSortRule maps the storage option onto the wire value of QueryAllFiles.
//
// The server only understands four orderings (see QueryAllFilesRequest):
// 0 = time desc (client default), 1 = name asc, 2 = name desc, 3 = time asc.
// Sorting by size does not exist server side, so the size_* options were
// removed instead of being silently mapped to a wrong numeric value.
func (d *Wopan) getSortRule() int {
	switch d.SortRule {
	case "name_asc":
		return wopansdk.SortNameAsc
	case "name_desc":
		return wopansdk.SortNameDesc
	case "time_asc":
		return wopansdk.SortTimeAsc
	case "time_desc":
		return wopansdk.SortTimeDesc
	default:
		return wopansdk.SortTimeDesc
	}
}

// getVipLevel returns the membership level and caches it for the storage.
//
// DeleteFile sends vipLevel on every request and the official client fills it
// from the membership state it loads at startup; hardcoding "0" makes the
// server treat a paying account as a free one.
func (d *Wopan) getVipLevel(ctx context.Context) string {
	if d.vipLevel != "" {
		return d.vipLevel
	}
	d.vipLevel = "0"
	quota, err := d.client.QueryCloudUsageInfo(func(req *resty.Request) {
		req.SetContext(ctx)
	})
	if err == nil && quota.VipLevel != "" {
		d.vipLevel = quota.VipLevel
	}
	return d.vipLevel
}

// 20230607214351
func getTime(str string) (time.Time, error) {
	loc := time.FixedZone("UTC+8", 8*60*60)
	return time.ParseInLocation("20060102150405", str, loc)
}
