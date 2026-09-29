// Package deps pins every third-party module the port uses, so go mod tidy keeps them in go.mod before any package
// imports them. See PORTING.md for which library serves which format. Only the go.mod owner edits this file.
package deps

import (
	_ "github.com/PuerkitoBio/goquery"               // HTML
	_ "github.com/adhocore/gronx"                    // cron expressions (bin/schedule, publishes_missed)
	_ "github.com/getkin/kin-openapi/openapi3"       // OpenAPI response validation in API tests
	_ "github.com/getkin/kin-openapi/openapi3filter" // validates responses against lib/public/v2/openapi.json
	_ "github.com/getkin/kin-openapi/routers/legacy" // finds the operation for a request
	_ "github.com/klippa-app/go-pdfium/webassembly"  // PDF, via internal/pdftext
	_ "github.com/shakinm/xlsReader/xls"             // legacy .xls, via internal/xls
	_ "github.com/xuri/excelize/v2"                  // .xlsx
	_ "go.yaml.in/yaml/v4"                           // YAML (cassettes)
	_ "golang.org/x/net/html/charset"                // charset detection for HTML/XML bodies
	_ "golang.org/x/text/encoding/charmap"           // Windows-1251, ISO-8859-1 and other legacy encodings
	_ "gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"       // cassette replay in tests
	_ "modernc.org/sqlite"                           // SQLite, pure Go
)
