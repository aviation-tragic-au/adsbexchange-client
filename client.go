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

// Package adsbexchange is a client for ADS-B Exchange's API on RapidAPI,
// returning aircraft in readsb's v2 format (the adsbv2 module).
package adsbexchange

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aviation-tragic-au/adsbv2"
	"go.uber.org/zap"
)

const (
	// DefaultHost is ADS-B Exchange's RapidAPI host, used when Config.Host
	// is empty.
	DefaultHost = "adsbexchange-com1.p.rapidapi.com"

	// DefaultTimeout bounds a request when Config.HTTPClient is unset.
	DefaultTimeout = 10 * time.Second

	// MaxDistNM is the largest radius GetAircraftWithinRange accepts.
	MaxDistNM = 250

	headerRapidAPIKey  = "X-RapidAPI-Key"
	headerRapidAPIHost = "X-RapidAPI-Host"

	headerQuotaLimit     = "X-RateLimit-Requests-Limit"
	headerQuotaRemaining = "X-RateLimit-Requests-Remaining"
)

var (
	// ErrNotFound is returned by the single-aircraft lookups when ADS-B
	// Exchange has no aircraft for the code.
	ErrNotFound = errors.New("adsbexchange: aircraft not found")

	// ErrRateLimited matches, under errors.Is, a 429 from RapidAPI. The
	// error is an *adsbv2.RateLimitError carrying the Retry-After delay.
	ErrRateLimited = adsbv2.ErrRateLimited

	squawkPattern = regexp.MustCompile(`^[0-7]{4}$`)
)

// Client is a client for ADS-B Exchange's API. It is safe for concurrent
// use. Every request counts against the RapidAPI plan's allowance; see
// Quota.
type Client struct {
	logger     *zap.Logger
	httpClient *http.Client
	key        func() string
	url        string
	host       string

	quota atomic.Pointer[Quota]
}

// NewClient creates an ADS-B Exchange client. A nil logger logs nothing.
func NewClient(logger *zap.Logger, cfg Config) (*Client, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	host := cfg.Host
	if host == "" {
		host = DefaultHost
	}

	base := strings.TrimRight(cfg.URL, "/")
	if base == "" {
		base = "https://" + host + "/v2"
	}
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("adsbexchange: invalid URL: %w", err)
	}

	key := cfg.KeyFunc
	if key == nil {
		static := cfg.Key
		key = func() string { return static }
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}

	return &Client{
		logger:     logger.Named("adsbexchange"),
		httpClient: httpClient,
		key:        key,
		url:        base,
		host:       host,
	}, nil
}

// Quota returns the plan allowance RapidAPI reported on the latest
// response that carried it, or nil before any has.
func (c *Client) Quota() *Quota {
	return c.quota.Load()
}

// GetAircraftWithinRange returns every aircraft within distNM nautical
// miles (1 to MaxDistNM) of lat/lon, with RapidAPI's quota headers when
// sent. The quota comes back even when the request failed (a 429, say), so
// a caller can meter its usage either way.
func (c *Client) GetAircraftWithinRange(ctx context.Context, lat, lon float64, distNM int) ([]*Aircraft, *Quota, error) {
	if !valid(lat, 90) || !valid(lon, 180) {
		return nil, nil, fmt.Errorf("adsbexchange: invalid coordinates %v,%v", lat, lon)
	}
	if distNM <= 0 || distNM > MaxDistNM {
		return nil, nil, fmt.Errorf("adsbexchange: distance must be 1-%d nm, got %d", MaxDistNM, distNM)
	}

	out, quota, err := c.query(ctx, fmt.Sprintf("/lat/%.5f/lon/%.5f/dist/%d/", lat, lon, distNM))
	if err != nil {
		return nil, quota, err
	}
	return out.Aircraft, quota, nil
}

// GetMilitaryAircraft returns every aircraft the database marks as
// military.
func (c *Client) GetMilitaryAircraft(ctx context.Context) ([]*Aircraft, error) {
	out, _, err := c.query(ctx, "/mil/")
	if err != nil {
		return nil, err
	}
	return out.Aircraft, nil
}

// GetAircraftBySquawk returns the aircraft squawking a code: four octal
// digits, as a string so a leading zero ("0123") survives.
func (c *Client) GetAircraftBySquawk(ctx context.Context, squawk string) ([]*Aircraft, error) {
	if !squawkPattern.MatchString(squawk) {
		return nil, fmt.Errorf("adsbexchange: invalid squawk %q", squawk)
	}

	out, _, err := c.query(ctx, "/sqk/"+squawk+"/")
	if err != nil {
		return nil, err
	}
	return out.Aircraft, nil
}

// GetAircraftByCallsign returns the aircraft using a callsign, or
// ErrNotFound.
func (c *Client) GetAircraftByCallsign(ctx context.Context, callsign string) (*Aircraft, error) {
	return c.lookup(ctx, "callsign", strings.ToUpper(strings.TrimSpace(callsign)))
}

// GetAircraftByHex returns the aircraft with a 24-bit ICAO address in hex
// ("7c6b2d"), or ErrNotFound.
func (c *Client) GetAircraftByHex(ctx context.Context, hex string) (*Aircraft, error) {
	return c.lookup(ctx, "hex", strings.ToLower(strings.TrimSpace(hex)))
}

// GetAircraftByICAO returns the aircraft with a 24-bit ICAO address, or
// ErrNotFound. ADS-B Exchange serves it as an alias of GetAircraftByHex.
func (c *Client) GetAircraftByICAO(ctx context.Context, icao string) (*Aircraft, error) {
	return c.lookup(ctx, "icao", strings.ToLower(strings.TrimSpace(icao)))
}

// GetAircraftByRegistration returns the aircraft with a registration
// ("VH-VZS"), or ErrNotFound.
func (c *Client) GetAircraftByRegistration(ctx context.Context, registration string) (*Aircraft, error) {
	return c.lookup(ctx, "registration", strings.ToUpper(strings.TrimSpace(registration)))
}

// FilterRecordTypes returns the aircraft whose RecordType is one of types,
// such as adsbv2.RecordTypeADSBICAO and adsbv2.RecordTypeMLAT to keep only
// transponder and multilateration positions.
func FilterRecordTypes(aircraft []*Aircraft, types ...string) []*Aircraft {
	var out []*Aircraft
	for _, ac := range aircraft {
		for _, t := range types {
			if ac.RecordType == t {
				out = append(out, ac)
				break
			}
		}
	}
	return out
}

// lookup fetches /<kind>/<code>/ and returns its first aircraft. ADS-B
// Exchange wraps even a single aircraft in the {"ac": [...]} envelope.
func (c *Client) lookup(ctx context.Context, kind, code string) (*Aircraft, error) {
	if code == "" {
		return nil, fmt.Errorf("adsbexchange: a %s is required", kind)
	}

	out, _, err := c.query(ctx, "/"+kind+"/"+url.PathEscape(code)+"/")
	if err != nil {
		return nil, err
	}
	if len(out.Aircraft) == 0 || out.Aircraft[0] == nil {
		return nil, ErrNotFound
	}
	return out.Aircraft[0], nil
}

// query sends a GET for path and decodes the v2 envelope, recording the
// quota headers whatever the outcome.
func (c *Client) query(ctx context.Context, path string) (*Response, *Quota, error) {
	key := c.key()
	if key == "" {
		return nil, nil, errors.New("adsbexchange: no API key set")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+path, http.NoBody)
	if err != nil {
		return nil, nil, fmt.Errorf("adsbexchange: unable to build request: %w", err)
	}
	req.Header.Set(headerRapidAPIKey, key)
	req.Header.Set(headerRapidAPIHost, c.host)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("adsbexchange: request failed: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	quota := readQuota(res.Header)
	if quota != nil {
		c.quota.Store(quota)
	}

	out, err := adsbv2.Decode(res)
	if err != nil {
		c.logger.Debug("request failed", zap.String("path", path), zap.Error(err))
		return nil, quota, fmt.Errorf("adsbexchange: %w", err)
	}

	return out, quota, nil
}

func readQuota(h http.Header) *Quota {
	q := &Quota{Limit: headerInt(h, headerQuotaLimit), Remaining: headerInt(h, headerQuotaRemaining)}
	if q.Limit == nil && q.Remaining == nil {
		return nil
	}
	return q
}

func headerInt(h http.Header, name string) *int {
	v, err := strconv.Atoi(strings.TrimSpace(h.Get(name)))
	if err != nil {
		return nil
	}
	return &v
}

// valid reports whether v is a finite number within ±limit.
func valid(v, limit float64) bool {
	return !math.IsNaN(v) && v >= -limit && v <= limit
}
