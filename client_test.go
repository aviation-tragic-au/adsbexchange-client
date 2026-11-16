// Copyright 2026 Dean Stalker
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package adsbexchange

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aviation-tragic-au/adsbv2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// server starts a test server that checks the path and the RapidAPI
// headers, then answers with status and body. It returns a client pointed
// at it.
func server(t *testing.T, path string, status int, body string, header http.Header) *Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, path, r.URL.EscapedPath())
		assert.Equal(t, "test-key", r.Header.Get(headerRapidAPIKey))
		assert.Equal(t, DefaultHost, r.Header.Get(headerRapidAPIHost))
		for k, v := range header {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(nil, Config{URL: srv.URL + "/v2", Key: "test-key"})
	require.NoError(t, err)
	return c
}

func TestNewClient(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		c, err := NewClient(nil, Config{Key: "k"})
		require.NoError(t, err)
		assert.Equal(t, "https://"+DefaultHost+"/v2", c.url)
		assert.Equal(t, DefaultHost, c.host)
		assert.Equal(t, DefaultTimeout, c.httpClient.Timeout)
		assert.Nil(t, c.Quota())
	})

	t.Run("host gives the URL", func(t *testing.T) {
		c, err := NewClient(nil, Config{Host: "api.example.test", Key: "k"})
		require.NoError(t, err)
		assert.Equal(t, "https://api.example.test/v2", c.url)
	})

	t.Run("invalid URL", func(t *testing.T) {
		_, err := NewClient(nil, Config{URL: "http://bad\x7f"})
		require.Error(t, err)
	})

	t.Run("own HTTP client", func(t *testing.T) {
		hc := &http.Client{}
		c, err := NewClient(nil, Config{HTTPClient: hc})
		require.NoError(t, err)
		assert.Same(t, hc, c.httpClient)
	})
}

// TestGetAircraftWithinRange decodes the v2 {"ac": [...]} shape, keeps every
// record type, and reads the quota headers.
func TestGetAircraftWithinRange(t *testing.T) {
	quotaHeader := http.Header{headerQuotaLimit: {"10000"}, headerQuotaRemaining: {"8765"}}
	c := server(t, "/v2/lat/-27.38420/lon/153.11750/dist/50/", 200, `{"ac":[
		{"hex":"7c7a3b","type":"adsb_icao","flight":"VOZ1385 ","alt_baro":3500,"lat":-27.1,"lon":153.0},
		{"hex":"7c1089","type":"mlat","flight":"DJV","alt_baro":"ground","lat":-27.3,"lon":153.1},
		{"hex":"~1234ab","type":"tisb_other","alt_baro":1200}
	],"msg":"No error","now":1,"total":3}`, quotaHeader)

	aircraft, quota, err := c.GetAircraftWithinRange(context.Background(), -27.3842, 153.1175, 50)
	require.NoError(t, err)
	require.Len(t, aircraft, 3)

	assert.Equal(t, "VOZ1385", aircraft[0].Callsign())
	ft, ground := aircraft[0].Altitude()
	require.NotNil(t, ft)
	assert.Equal(t, 3500, *ft)
	assert.False(t, ground)
	_, ground = aircraft[1].Altitude()
	assert.True(t, ground)

	require.NotNil(t, quota)
	assert.Equal(t, 10000, *quota.Limit)
	assert.Equal(t, 8765, *quota.Remaining)
	assert.Equal(t, quota, c.Quota())
}

func TestGetAircraftWithinRangeValidation(t *testing.T) {
	c, err := NewClient(nil, Config{URL: "http://unused.test", Key: "k"})
	require.NoError(t, err)

	tests := []struct {
		name     string
		lat, lon float64
		dist     int
	}{
		{name: "zero distance", lat: 1, lon: 2, dist: 0},
		{name: "too far", lat: 1, lon: 2, dist: MaxDistNM + 1},
		{name: "latitude", lat: 90.1, lon: 2, dist: 10},
		{name: "longitude", lat: 1, lon: -181, dist: 10},
		{name: "NaN", lat: math.NaN(), lon: 2, dist: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := c.GetAircraftWithinRange(context.Background(), tt.lat, tt.lon, tt.dist)
			require.Error(t, err)
		})
	}
}

// TestRateLimited maps a 429 to ErrRateLimited, with its Retry-After, and
// still returns and records the quota.
func TestRateLimited(t *testing.T) {
	c := server(t, "/v2/lat/1.00000/lon/2.00000/dist/50/", 429, "",
		http.Header{"Retry-After": {"30"}, headerQuotaLimit: {"10000"}, headerQuotaRemaining: {"0"}})

	_, quota, err := c.GetAircraftWithinRange(context.Background(), 1, 2, 50)
	require.ErrorIs(t, err, ErrRateLimited)
	var rl *adsbv2.RateLimitError
	require.ErrorAs(t, err, &rl)
	assert.Equal(t, 30*time.Second, rl.RetryAfter)

	require.NotNil(t, quota)
	assert.Equal(t, 0, *quota.Remaining)
	assert.Equal(t, quota, c.Quota())
}

func TestServerError(t *testing.T) {
	c := server(t, "/v2/mil/", 502, "bad gateway", nil)

	_, err := c.GetMilitaryAircraft(context.Background())
	var se *adsbv2.StatusError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, 502, se.StatusCode)
	assert.NotErrorIs(t, err, ErrRateLimited)
}

func TestGetMilitaryAircraft(t *testing.T) {
	c := server(t, "/v2/mil/", 200, `{"ac":[{"hex":"7cf9c1","dbFlags":1},{"hex":"ae1234","dbFlags":1}]}`, nil)

	aircraft, err := c.GetMilitaryAircraft(context.Background())
	require.NoError(t, err)
	require.Len(t, aircraft, 2)
	assert.True(t, aircraft[0].IsMilitary())
}

func TestGetAircraftBySquawk(t *testing.T) {
	c := server(t, "/v2/sqk/0123/", 200, `{"ac":[{"hex":"7c7a3b","squawk":"0123"}]}`, nil)

	aircraft, err := c.GetAircraftBySquawk(context.Background(), "0123")
	require.NoError(t, err)
	require.Len(t, aircraft, 1)
	assert.Equal(t, "0123", aircraft[0].Squawk)

	for _, bad := range []string{"", "123", "12345", "7800", "ab12"} {
		_, err := c.GetAircraftBySquawk(context.Background(), bad)
		require.ErrorContains(t, err, "invalid squawk", bad)
	}
}

// TestLookups covers the single-aircraft lookups, which ADS-B Exchange
// answers in the same {"ac": [...]} envelope as a search.
func TestLookups(t *testing.T) {
	type lookup func(*Client, context.Context, string) (*Aircraft, error)
	byCallsign := (*Client).GetAircraftByCallsign
	byHex := (*Client).GetAircraftByHex
	byICAO := (*Client).GetAircraftByICAO
	byReg := (*Client).GetAircraftByRegistration

	tests := []struct {
		name    string
		fn      lookup
		code    string
		path    string
		body    string
		wantHex string
		wantErr error
		errText string
	}{
		{name: "callsign", fn: byCallsign, code: " qfa615 ", path: "/v2/callsign/QFA615/", body: `{"ac":[{"hex":"7c6b2d"}]}`, wantHex: "7c6b2d"},
		{name: "hex", fn: byHex, code: "7C6B2D", path: "/v2/hex/7c6b2d/", body: `{"ac":[{"hex":"7c6b2d"}]}`, wantHex: "7c6b2d"},
		{name: "icao", fn: byICAO, code: "7c6b2d", path: "/v2/icao/7c6b2d/", body: `{"ac":[{"hex":"7c6b2d"}]}`, wantHex: "7c6b2d"},
		{name: "registration", fn: byReg, code: "vh-vzs", path: "/v2/registration/VH-VZS/", body: `{"ac":[{"hex":"7c6b2d"}]}`, wantHex: "7c6b2d"},
		{name: "escaped", fn: byReg, code: "A/B", path: "/v2/registration/A%2FB/", body: `{"ac":[{"hex":"1"}]}`, wantHex: "1"},
		{name: "none", fn: byHex, code: "000000", path: "/v2/hex/000000/", body: `{"ac":[],"msg":"No error"}`, wantErr: ErrNotFound},
		{name: "null list", fn: byHex, code: "000000", path: "/v2/hex/000000/", body: `{"ac":null}`, wantErr: ErrNotFound},
		{name: "empty code", fn: byCallsign, code: "  ", errText: "a callsign is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := server(t, tt.path, 200, tt.body, nil)

			ac, err := tt.fn(c, context.Background(), tt.code)

			switch {
			case tt.wantErr != nil:
				require.ErrorIs(t, err, tt.wantErr)
			case tt.errText != "":
				require.ErrorContains(t, err, tt.errText)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.wantHex, ac.Hex)
			}
		})
	}
}

func TestFilterRecordTypes(t *testing.T) {
	in := []*Aircraft{
		{Hex: "a", RecordType: adsbv2.RecordTypeADSBICAO},
		{Hex: "b", RecordType: adsbv2.RecordTypeMLAT},
		{Hex: "c", RecordType: adsbv2.RecordTypeTISBOther},
	}

	out := FilterRecordTypes(in, adsbv2.RecordTypeADSBICAO, adsbv2.RecordTypeMLAT)
	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0].Hex)
	assert.Equal(t, "b", out[1].Hex)

	assert.Empty(t, FilterRecordTypes(in))
	assert.Empty(t, FilterRecordTypes(nil, adsbv2.RecordTypeMLAT))
}

func TestKeyFuncReadPerRequest(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get(headerRapidAPIKey))
		_, _ = io.WriteString(w, `{"ac":[]}`)
	}))
	defer srv.Close()

	var n atomic.Int32
	keys := []string{"first", "second"}
	c, err := NewClient(nil, Config{URL: srv.URL, KeyFunc: func() string { return keys[n.Add(1)-1] }})
	require.NoError(t, err)

	_, _ = c.GetMilitaryAircraft(context.Background())
	_, _ = c.GetMilitaryAircraft(context.Background())

	assert.Equal(t, []string{"first", "second"}, seen)
}

// TestNoKey fails the request, not the client: the key can arrive later
// through KeyFunc.
func TestNoKey(t *testing.T) {
	c, err := NewClient(nil, Config{URL: "http://unused.test"})
	require.NoError(t, err)

	_, err = c.GetMilitaryAircraft(context.Background())
	require.ErrorContains(t, err, "no API key set")
}

func TestCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c, err := NewClient(nil, Config{URL: srv.URL, Key: "k"})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = c.GetMilitaryAircraft(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
