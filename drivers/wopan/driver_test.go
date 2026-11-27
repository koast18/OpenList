package wopandriver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	wopansdk "github.com/OpenListTeam/wopan-sdk-go"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// woPanMock mimics the WoPan dispatcher: it answers with the documented
// envelope and rejects requests whose User-Agent is not a browser UA, exactly
// like the edge does when the driver forgets wopansdk.DefaultUA.
type woPanMock struct {
	calls     []string
	refreshed int
	badUA     int
	// rejectRefreshes is how many AppRefreshToken calls answer "login failed"
	// (RSP_CODE 8005) before one succeeds, which is how a wrong client
	// identity looks from the outside.
	rejectRefreshes int
}

func (m *woPanMock) transport(t *testing.T) http.RoundTripper {
	t.Helper()
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if ua := r.Header.Get("User-Agent"); ua != wopansdk.DefaultUA {
			m.badUA++
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				Body:       io.NopCloser(strings.NewReader("forbidden")),
				Header:     make(http.Header),
				Request:    r,
			}, nil
		}

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var req struct {
			Header struct {
				Key string `json:"key"`
			} `json:"header"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		m.calls = append(m.calls, req.Header.Key)

		rspCode, rspDesc, data := "0000", "", "{}"
		switch req.Header.Key {
		case wopansdk.KeyAppRefreshToken:
			m.refreshed++
			if m.refreshed <= m.rejectRefreshes {
				rspCode, rspDesc = "8005", "登录失败"
				data = "null"
			} else {
				data = `{"access_token":"fedcba9876543210","refresh_token":"new-refresh"}`
			}
		case wopansdk.KeyAppLoginAccount:
			data = `{"access_token":"login-access-token","refresh_token":"login-refresh","phone":"13800000000"}`
		case wopansdk.KeyAppQueryUser:
			data = `{"userId":"test-user"}`
		case wopansdk.KeyClassifyRule:
			data = `{"fileTypes":{}}`
		case wopansdk.KeyGetZoneInfo:
			data = `{"url":"https://example.invalid"}`
		case wopansdk.KeyFamilyUserCurrentEncode:
			data = `{"defaultHomeId":42}`
		case wopansdk.KeyQueryAllFiles:
			data = `{"files":[]}`
		}
		body := `{"STATUS":"200","RSP":{"RSP_CODE":"` + rspCode + `","RSP_DESC":"` + rspDesc + `","DATA":` + data + `}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    r,
		}, nil
	})
}

// newTestClient mirrors Wopan.Init, but injects a mock transport and a token
// callback that does not touch the database.
func newTestClient(d *Wopan, rt http.RoundTripper) {
	d.client = wopansdk.DefaultWithRefreshToken(d.RefreshToken)
	d.client.SetAccessToken(d.AccessToken)
	d.client.SetPhone(d.Phone)
	d.client.SetHttpClient(&http.Client{Transport: rt})
	d.client.OnRefreshToken(d.setTokens)
	d.applyClientCredentials(d.preferredClientMark())
}

func TestInitClientCredentials(t *testing.T) {
	const validAccessToken = "0123456789abcdef"

	tests := []struct {
		name            string
		accessToken     string
		refreshToken    string
		wantRefresh     bool
		wantErr         bool
		wantAccessToken string
		wantFamilyID    string
		wantMark        string
	}{
		{
			name:            "refresh only",
			refreshToken:    "refresh",
			wantRefresh:     true,
			wantAccessToken: "fedcba9876543210",
			wantFamilyID:    "42",
			wantMark:        "pc_web",
		},
		{
			name:            "short access token with refresh",
			accessToken:     "short",
			refreshToken:    "refresh",
			wantRefresh:     true,
			wantAccessToken: "fedcba9876543210",
			wantFamilyID:    "42",
			wantMark:        "pc_web",
		},
		{
			name:            "both tokens",
			accessToken:     validAccessToken,
			refreshToken:    "refresh",
			wantAccessToken: validAccessToken,
			wantFamilyID:    "42",
			wantMark:        "pc_web",
		},
		{
			name:            "access token only",
			accessToken:     validAccessToken,
			wantAccessToken: validAccessToken,
			wantFamilyID:    "42",
			wantMark:        "pc_web",
		},
		{
			name:    "no credentials",
			wantErr: true,
		},
		{
			name:        "short access token without refresh",
			accessToken: "short",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &woPanMock{}
			d := &Wopan{Addition: Addition{
				AccessToken:  tt.accessToken,
				RefreshToken: tt.refreshToken,
			}}
			newTestClient(d, mock.transport(t))

			err := d.initClient(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if len(mock.calls) != 0 {
					t.Fatalf("expected no dispatcher call on configuration error, got %v", mock.calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("initClient returned error: %v", err)
			}
			if mock.badUA != 0 {
				t.Fatalf("%d request(s) were sent without the browser user agent", mock.badUA)
			}
			if (mock.refreshed > 0) != tt.wantRefresh {
				t.Fatalf("refresh called = %d, want %v (calls: %v)", mock.refreshed, tt.wantRefresh, mock.calls)
			}
			if tt.wantRefresh && mock.calls[0] != wopansdk.KeyAppRefreshToken {
				t.Fatalf("first call = %q, want %q", mock.calls[0], wopansdk.KeyAppRefreshToken)
			}
			if d.AccessToken != tt.wantAccessToken {
				t.Fatalf("AccessToken = %q, want %q", d.AccessToken, tt.wantAccessToken)
			}
			if tt.wantRefresh && d.RefreshToken != "new-refresh" {
				t.Fatalf("RefreshToken = %q, want new-refresh", d.RefreshToken)
			}
			if d.defaultFamilyID != tt.wantFamilyID {
				t.Fatalf("defaultFamilyID = %q, want %q", d.defaultFamilyID, tt.wantFamilyID)
			}
			if d.clientMarkResolved != tt.wantMark {
				t.Fatalf("clientMarkResolved = %q, want %q", d.clientMarkResolved, tt.wantMark)
			}
		})
	}
}

// TestInitClientIdentityFallback covers the case the whole fork exists for: a
// token minted by the Android app cannot be refreshed with the PC/Web client
// pair, and the driver must find that out by itself.
func TestInitClientIdentityFallback(t *testing.T) {
	mock := &woPanMock{rejectRefreshes: 1}
	d := &Wopan{Addition: Addition{RefreshToken: "app-refresh-token"}}
	newTestClient(d, mock.transport(t))

	if err := d.initClient(context.Background()); err != nil {
		t.Fatalf("initClient returned error: %v", err)
	}
	if mock.refreshed != 2 {
		t.Fatalf("refresh attempts = %d, want 2 (one per client identity)", mock.refreshed)
	}
	if d.clientMarkResolved != "android" {
		t.Fatalf("clientMarkResolved = %q, want android", d.clientMarkResolved)
	}
	if got := d.client.ClientID(); got != wopansdk.ClientIDAndroid {
		t.Fatalf("clientId = %q, want %q", got, wopansdk.ClientIDAndroid)
	}
}

// TestInitClientBuiltinLogin covers phone+password: the driver logs in with the
// Android client identity instead of asking for a token.
func TestInitClientBuiltinLogin(t *testing.T) {
	mock := &woPanMock{}
	d := &Wopan{Addition: Addition{Phone: "13800000000", Password: "secret"}}
	newTestClient(d, mock.transport(t))

	if err := d.initClient(context.Background()); err != nil {
		t.Fatalf("initClient returned error: %v", err)
	}
	if len(mock.calls) == 0 || mock.calls[0] != wopansdk.KeyAppLoginAccount {
		t.Fatalf("first call = %v, want %q", mock.calls, wopansdk.KeyAppLoginAccount)
	}
	if got := d.client.ClientID(); got != wopansdk.ClientIDAndroid {
		t.Fatalf("clientId = %q, want %q", got, wopansdk.ClientIDAndroid)
	}
}

func TestSpaceAndSortMapping(t *testing.T) {
	spaces := map[string]string{
		"":                wopansdk.SpaceTypePersonal,
		"personal":        wopansdk.SpaceTypePersonal,
		"family":          wopansdk.SpaceTypeFamily,
		"private":         wopansdk.SpaceTypePrivate,
		"wechat_backup":   wopansdk.SpaceTypeWechatBack,
		"album_backup":    wopansdk.SpaceTypeAlbumBackup,
		"media_square":    wopansdk.SpaceTypeMediaSquare,
		"not-a-real-name": wopansdk.SpaceTypePersonal,
	}
	for in, want := range spaces {
		d := &Wopan{Addition: Addition{Space: in}}
		if got := d.getSpaceType(); got != want {
			t.Fatalf("getSpaceType(%q) = %q, want %q", in, got, want)
		}
	}

	// A storage configured before the space option existed used to switch to
	// the family space by filling family_id; that must keep working.
	legacy := &Wopan{Addition: Addition{FamilyID: "9"}}
	if got := legacy.getSpaceType(); got != wopansdk.SpaceTypeFamily {
		t.Fatalf("legacy getSpaceType() = %q, want %q", got, wopansdk.SpaceTypeFamily)
	}

	sorts := map[string]int{
		"":          wopansdk.SortTimeDesc,
		"time_desc": wopansdk.SortTimeDesc,
		"name_asc":  wopansdk.SortNameAsc,
		"name_desc": wopansdk.SortNameDesc,
		"time_asc":  wopansdk.SortTimeAsc,
		// options removed from the form must not fall through to a wrong value
		"size_asc": wopansdk.SortTimeDesc,
	}
	for in, want := range sorts {
		d := &Wopan{Addition: Addition{SortRule: in}}
		if got := d.getSortRule(); got != want {
			t.Fatalf("getSortRule(%q) = %d, want %d", in, got, want)
		}
	}
}
