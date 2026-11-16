# adsbexchange-client

[![Lint](https://github.com/aviation-tragic-au/adsbexchange-client/actions/workflows/lint.yml/badge.svg)](https://github.com/aviation-tragic-au/adsbexchange-client/actions/workflows/lint.yml) [![Test](https://github.com/aviation-tragic-au/adsbexchange-client/actions/workflows/test.yml/badge.svg)](https://github.com/aviation-tragic-au/adsbexchange-client/actions/workflows/test.yml)

Provides a client for the Community API provided by ADSB Exchange. This will not work for their Commercial API.

https://rapidapi.com/adsbx/api/adsbexchange-com1

## Install

```sh
go get github.com/aviation-tragic-au/adsbexchange-client
```

## Usage

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "log"

    adsbexchange "github.com/aviation-tragic-au/adsbexchange-client"
    "go.uber.org/zap"
)

func main() {
    logger, _ := zap.NewProduction()

    client, err := adsbexchange.NewClient(logger, adsbexchange.Config{
        Key: "your-rapidapi-key",
    })
    if err != nil {
        log.Fatal(err)
    }

    // Everything within 25 nm of Brisbane Airport, and the plan allowance
    // RapidAPI reported with it
    aircraft, quota, err := client.GetAircraftWithinRange(context.Background(), -27.3842, 153.1175, 25)
    if errors.Is(err, adsbexchange.ErrRateLimited) {
        log.Fatal("over the plan's allowance")
    }
    if err != nil {
        log.Fatal(err)
    }
    if quota != nil && quota.Remaining != nil {
        log.Printf("%d requests left", *quota.Remaining)
    }

    for _, ac := range aircraft {
        feet, onGround := ac.Altitude()
        switch {
        case onGround:
            fmt.Printf("%s (%s) on the ground\n", ac.Callsign(), ac.Hex)
        case feet != nil:
            fmt.Printf("%s (%s) at %d ft\n", ac.Callsign(), ac.Hex, *feet)
        }
    }
}
```

Aircraft are [`adsbv2.Aircraft`](https://github.com/aviation-tragic-au/adsbv2),
readsb's v2 format, shared with the clients for the other readsb-based
sources.

### Methods

Every method takes a `context.Context` first.

| Method | Returns |
|---|---|
| `GetAircraftWithinRange(ctx, lat, lon, distNM)` | Every aircraft within 1 to 250 nm, and the quota |
| `GetMilitaryAircraft(ctx)` | Aircraft the database marks as military |
| `GetAircraftBySquawk(ctx, squawk)` | Aircraft squawking a code (four octal digits, as a string) |
| `GetAircraftByCallsign(ctx, callsign)` | One aircraft, or `ErrNotFound` |
| `GetAircraftByHex(ctx, hex)` / `GetAircraftByICAO(ctx, icao)` | One aircraft by 24-bit address, or `ErrNotFound` |
| `GetAircraftByRegistration(ctx, registration)` | One aircraft, or `ErrNotFound` |
| `Quota()` | The allowance RapidAPI reported on the latest response |

Results include every record type. `FilterRecordTypes(aircraft,
adsbv2.RecordTypeADSBICAO, adsbv2.RecordTypeMLAT)` keeps just transponder
and multilateration positions.

A 429 matches `ErrRateLimited` (it's an `*adsbv2.RateLimitError` with the
`Retry-After` delay); any other non-200 is an `*adsbv2.StatusError`.

### Configuration

| Field        | Description |
|--------------|-------------|
| `Key`        | Your RapidAPI key |
| `KeyFunc`    | Called on every request in place of `Key`, to pick up a key changed at runtime |
| `Host`       | RapidAPI host, sent as `X-RapidAPI-Host` (default `adsbexchange-com1.p.rapidapi.com`) |
| `URL`        | API base (default `https://<Host>/v2`) |
| `HTTPClient` | Your own `*http.Client` (the default times out after 10 seconds) |

`Config` carries `json` and `mapstructure` tags, so it can be decoded
straight from a config file or Viper; the client itself doesn't depend on
either.

## Contributing

1. Fork it
2. Create your feature branch (`git checkout -b my-new-feature`)
3. Commit your changes (`git commit -am 'Add some feature'`)
4. Push to the branch (`git push origin my-new-feature`)
5. Create a new Pull Request

## License

Copyright 2026 Dean Stalker

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
