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
	"net/http"

	"github.com/aviation-tragic-au/adsbv2"
)

// Aircraft is an aircraft in ADS-B Exchange's v2 format, shared with the
// other readsb-based sources through the adsbv2 module.
type Aircraft = adsbv2.Aircraft

// Response is a v2 query result.
type Response = adsbv2.Response

// Config configures a Client. Only a key (Key or KeyFunc) is required. The
// mapstructure tags let a host decode it straight from viper or similar.
type Config struct {
	// Host is the RapidAPI host, sent as X-RapidAPI-Host. Empty means
	// DefaultHost.
	Host string `json:"host" mapstructure:"host"`

	// URL is the API base. Empty means https://<Host>/v2.
	URL string `json:"url" mapstructure:"url"`

	// Key is the RapidAPI key.
	Key string `json:"key" mapstructure:"key"`

	// KeyFunc, when set, is called for every request in place of Key, so
	// a host can change the key (from a settings page, say) without
	// building a new client.
	KeyFunc func() string `json:"-" mapstructure:"-"`

	// HTTPClient sends the requests. Nil means a client with
	// DefaultTimeout.
	HTTPClient *http.Client `json:"-" mapstructure:"-"`
}

// Quota is RapidAPI's view of the plan's request allowance, read from its
// X-RateLimit-Requests-* response headers. A nil field wasn't sent.
type Quota struct {
	Limit     *int
	Remaining *int
}
