package output

// appendJSONString and the htmlSafeSet/hexDigit tables below are adapted
// from Go's encoding/json (BSD-3-Clause, part of the Go toolchain this
// binary is built with) — not an external dependency, copied verbatim for
// byte-exact compatibility with json.Marshal's default (HTML-escaping)
// output. See go1.26.5 src/encoding/json/{encode,tables}.go.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/commonhuman-lab/chcrawl/internal/extract"
	"github.com/commonhuman-lab/chcrawl/internal/fetch"
	"github.com/commonhuman-lab/chcrawl/internal/openapi"
)

const hexDigit = "0123456789abcdef"

// htmlSafeSet holds true for every ASCII byte that can appear unescaped
// inside a JSON string produced with HTML-escaping enabled (the default
// json.NewEncoder behavior, which this package must match). False for the
// ASCII control characters (0-31), '"', '\\', '<', '>', and '&'. Note that
// DEL (0x7f) is true — it is NOT escaped by the default encoder.
var htmlSafeSet = [utf8.RuneSelf]bool{
	' ':      true,
	'!':      true,
	'"':      false,
	'#':      true,
	'$':      true,
	'%':      true,
	'&':      false,
	'\'':     true,
	'(':      true,
	')':      true,
	'*':      true,
	'+':      true,
	',':      true,
	'-':      true,
	'.':      true,
	'/':      true,
	'0':      true,
	'1':      true,
	'2':      true,
	'3':      true,
	'4':      true,
	'5':      true,
	'6':      true,
	'7':      true,
	'8':      true,
	'9':      true,
	':':      true,
	';':      true,
	'<':      false,
	'=':      true,
	'>':      false,
	'?':      true,
	'@':      true,
	'A':      true,
	'B':      true,
	'C':      true,
	'D':      true,
	'E':      true,
	'F':      true,
	'G':      true,
	'H':      true,
	'I':      true,
	'J':      true,
	'K':      true,
	'L':      true,
	'M':      true,
	'N':      true,
	'O':      true,
	'P':      true,
	'Q':      true,
	'R':      true,
	'S':      true,
	'T':      true,
	'U':      true,
	'V':      true,
	'W':      true,
	'X':      true,
	'Y':      true,
	'Z':      true,
	'[':      true,
	'\\':     false,
	']':      true,
	'^':      true,
	'_':      true,
	'`':      true,
	'a':      true,
	'b':      true,
	'c':      true,
	'd':      true,
	'e':      true,
	'f':      true,
	'g':      true,
	'h':      true,
	'i':      true,
	'j':      true,
	'k':      true,
	'l':      true,
	'm':      true,
	'n':      true,
	'o':      true,
	'p':      true,
	'q':      true,
	'r':      true,
	's':      true,
	't':      true,
	'u':      true,
	'v':      true,
	'w':      true,
	'x':      true,
	'y':      true,
	'z':      true,
	'{':      true,
	'|':      true,
	'}':      true,
	'~':      true,
	'': true,
}

// appendJSONString appends s to dst as a double-quoted JSON string, using
// exactly the escaping rules of encoding/json's default encoder
// (SetEscapeHTML(true), which json.NewEncoder always uses): standard JSON
// escapes, \u00XX for other control bytes, � substitution for invalid
// UTF-8, and HTML-escaping of <, >, and &, plus U+2028/U+2029.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if htmlSafeSet[b] {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			switch b {
			case '\\', '"':
				dst = append(dst, '\\', b)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', hexDigit[b>>4], hexDigit[b&0xF])
			}
			i++
			start = i
			continue
		}
		c, size := utf8.DecodeRuneInString(s[i:])
		if c == utf8.RuneError && size == 1 {
			dst = append(dst, s[start:i]...)
			dst = append(dst, '\\', 'u', 'f', 'f', 'f', 'd')
			i += size
			start = i
			continue
		}
		// U+2028 LINE SEPARATOR, U+2029 PARAGRAPH SEPARATOR: valid in JSON
		// but not in JSONP; encoding/json escapes them unconditionally.
		if c == ' ' || c == ' ' {
			dst = append(dst, s[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hexDigit[c&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, s[start:]...)
	dst = append(dst, '"')
	return dst
}

// --- scalar encoders ---
//
// These append directly via strconv's Append* family (no fmt.Sprintf, no
// reflection) so the numeric/bool fast path pays nothing beyond what a
// hand-rolled encoder needs to.

func appendJSONInt(dst []byte, v int) []byte {
	return strconv.AppendInt(dst, int64(v), 10)
}

func appendJSONInt64(dst []byte, v int64) []byte {
	return strconv.AppendInt(dst, v, 10)
}

func appendJSONUint64(dst []byte, v uint64) []byte {
	return strconv.AppendUint(dst, v, 10)
}

func appendJSONBool(dst []byte, v bool) []byte {
	return strconv.AppendBool(dst, v)
}

// appendJSONFloat64 ports encoding/json's floatEncoder algorithm (the
// 'f'/'e' format cutoff and the e-09->e-9 exponent trim) so output matches
// json.Marshal exactly. Returns an error for NaN/Inf, as json.Marshal does.
func appendJSONFloat64(dst []byte, f float64) ([]byte, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return dst, fmt.Errorf("json: unsupported value: %s", strconv.FormatFloat(f, 'g', -1, 64))
	}
	abs := math.Abs(f)
	fmtByte := byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		fmtByte = 'e'
	}
	dst = strconv.AppendFloat(dst, f, fmtByte, -1, 64)
	if fmtByte == 'e' {
		if n := len(dst); n >= 4 && dst[n-4] == 'e' && dst[n-3] == '-' && dst[n-2] == '0' {
			dst[n-2] = dst[n-1]
			dst = dst[:n-1]
		}
	}
	return dst, nil
}

// appendJSONTime delegates to time.Time's own MarshalJSON (a concrete-type
// method call, not reflection) rather than reimplementing RFC3339Nano
// formatting and its edge cases (e.g. years outside [0,9999]).
func appendJSONTime(dst []byte, t time.Time) ([]byte, error) {
	b, err := t.MarshalJSON()
	if err != nil {
		return dst, err
	}
	return append(dst, b...), nil
}

// --- string slice / string map encoders ---
//
// Every slice/map field encoded here has no omitempty tag on its own type
// (RedirectHop, Param, Discovery, Endpoint carry no json tags at all), so a
// nil value must marshal to "null" and a non-nil empty value to "[]"/"{}" —
// collapsing that distinction is the most common way a hand-rolled encoder
// silently diverges from encoding/json.

func appendJSONStringSlice(dst []byte, s []string) []byte {
	if s == nil {
		return append(dst, "null"...)
	}
	dst = append(dst, '[')
	for i, v := range s {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendJSONString(dst, v)
	}
	return append(dst, ']')
}

func appendJSONStringMap(dst []byte, m map[string]string) []byte {
	if m == nil {
		return append(dst, "null"...)
	}
	dst = append(dst, '{')
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendJSONString(dst, k)
		dst = append(dst, ':')
		dst = appendJSONString(dst, m[k])
	}
	return append(dst, '}')
}

// --- nested cross-package type encoders ---
//
// fetch.RedirectHop, extract.Param, extract.Discovery, and openapi.Endpoint
// carry no json tags, so encoding/json marshals them under their literal Go
// field names. All fields on these types are always emitted (no omitempty
// anywhere here) — this package encodes them directly instead of adding
// AppendJSON methods to those packages, since internal/output is the only
// consumer that needs a JSON representation of them.

func appendRedirectHopJSON(dst []byte, h *fetch.RedirectHop) []byte {
	dst = append(dst, `{"URL":`...)
	dst = appendJSONString(dst, h.URL)
	dst = append(dst, `,"StatusCode":`...)
	dst = appendJSONInt(dst, h.StatusCode)
	return append(dst, '}')
}

func appendRedirectHopSliceJSON(dst []byte, hops []fetch.RedirectHop) []byte {
	if hops == nil {
		return append(dst, "null"...)
	}
	dst = append(dst, '[')
	for i := range hops {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendRedirectHopJSON(dst, &hops[i])
	}
	return append(dst, ']')
}

func appendParamJSON(dst []byte, p *extract.Param) []byte {
	dst = append(dst, `{"Name":`...)
	dst = appendJSONString(dst, p.Name)
	dst = append(dst, `,"Value":`...)
	dst = appendJSONString(dst, p.Value)
	return append(dst, '}')
}

func appendParamSliceJSON(dst []byte, params []extract.Param) []byte {
	if params == nil {
		return append(dst, "null"...)
	}
	dst = append(dst, '[')
	for i := range params {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendParamJSON(dst, &params[i])
	}
	return append(dst, ']')
}

func appendDiscoveryJSON(dst []byte, d *extract.Discovery) []byte {
	dst = append(dst, `{"Kind":`...)
	dst = appendJSONString(dst, d.Kind)
	dst = append(dst, `,"URL":`...)
	dst = appendJSONString(dst, d.URL)
	dst = append(dst, `,"Method":`...)
	dst = appendJSONString(dst, d.Method)
	dst = append(dst, `,"Params":`...)
	dst = appendParamSliceJSON(dst, d.Params)
	dst = append(dst, `,"Base":`...)
	dst = appendJSONStringMap(dst, d.Base)
	dst = append(dst, `,"Meta":`...)
	dst = appendJSONStringMap(dst, d.Meta)
	return append(dst, '}')
}

func appendDiscoverySliceJSON(dst []byte, discoveries []extract.Discovery) []byte {
	if discoveries == nil {
		return append(dst, "null"...)
	}
	dst = append(dst, '[')
	for i := range discoveries {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendDiscoveryJSON(dst, &discoveries[i])
	}
	return append(dst, ']')
}

func appendEndpointJSON(dst []byte, e *openapi.Endpoint) []byte {
	dst = append(dst, `{"URL":`...)
	dst = appendJSONString(dst, e.URL)
	dst = append(dst, `,"Method":`...)
	dst = appendJSONString(dst, e.Method)
	dst = append(dst, `,"PathParams":`...)
	dst = appendJSONStringSlice(dst, e.PathParams)
	dst = append(dst, `,"QueryParams":`...)
	dst = appendJSONStringSlice(dst, e.QueryParams)
	dst = append(dst, `,"BodyParams":`...)
	dst = appendJSONStringSlice(dst, e.BodyParams)
	dst = append(dst, `,"RawPath":`...)
	dst = appendJSONString(dst, e.RawPath)
	return append(dst, '}')
}

func appendEndpointSliceJSON(dst []byte, endpoints []openapi.Endpoint) []byte {
	if endpoints == nil {
		return append(dst, "null"...)
	}
	dst = append(dst, '[')
	for i := range endpoints {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendEndpointJSON(dst, &endpoints[i])
	}
	return append(dst, ']')
}

// --- top-level event encoders ---
//
// One per Writer method. Only PageEvent (time.Time) and SummaryEvent
// (float64, which can be NaN/Inf) can fail; ErrorEvent and OpenAPIEvent
// return plain []byte since nothing in their transitive field set is
// fallible.

func appendPageEventJSON(dst []byte, e *PageEvent) ([]byte, error) {
	dst = append(dst, `{"type":`...)
	dst = appendJSONString(dst, e.Type)
	dst = append(dst, `,"ts":`...)
	var err error
	dst, err = appendJSONTime(dst, e.Timestamp)
	if err != nil {
		return dst, err
	}
	dst = append(dst, `,"url":`...)
	dst = appendJSONString(dst, e.URL)
	if e.FinalURL != "" {
		dst = append(dst, `,"final_url":`...)
		dst = appendJSONString(dst, e.FinalURL)
	}
	dst = append(dst, `,"depth":`...)
	dst = appendJSONInt(dst, e.Depth)
	dst = append(dst, `,"status":`...)
	dst = appendJSONInt(dst, e.Status)
	if e.ContentType != "" {
		dst = append(dst, `,"content_type":`...)
		dst = appendJSONString(dst, e.ContentType)
	}
	dst = append(dst, `,"bytes_read":`...)
	dst = appendJSONInt(dst, e.BytesRead)
	if e.Truncated {
		dst = append(dst, `,"truncated":`...)
		dst = appendJSONBool(dst, e.Truncated)
	}
	if len(e.RedirectChain) > 0 {
		dst = append(dst, `,"redirect_chain":`...)
		dst = appendRedirectHopSliceJSON(dst, e.RedirectChain)
	}
	if len(e.Discoveries) > 0 {
		dst = append(dst, `,"discoveries":`...)
		dst = appendDiscoverySliceJSON(dst, e.Discoveries)
	}
	dst = append(dst, `,"fetch_ms":`...)
	dst = appendJSONInt64(dst, e.FetchMS)
	if e.RetryAttempts != 0 {
		dst = append(dst, `,"retry_attempts":`...)
		dst = appendJSONInt(dst, e.RetryAttempts)
	}
	if e.RetryDelayMS != 0 {
		dst = append(dst, `,"retry_delay_ms":`...)
		dst = appendJSONInt64(dst, e.RetryDelayMS)
	}
	dst = append(dst, '}')
	return dst, nil
}

func appendErrorEventJSON(dst []byte, e *ErrorEvent) []byte {
	dst = append(dst, `{"type":`...)
	dst = appendJSONString(dst, e.Type)
	dst = append(dst, `,"url":`...)
	dst = appendJSONString(dst, e.URL)
	dst = append(dst, `,"stage":`...)
	dst = appendJSONString(dst, e.Stage)
	dst = append(dst, `,"error":`...)
	dst = appendJSONString(dst, e.Error)
	if e.RetryAttempts != 0 {
		dst = append(dst, `,"retry_attempts":`...)
		dst = appendJSONInt(dst, e.RetryAttempts)
	}
	if e.RetryDelayMS != 0 {
		dst = append(dst, `,"retry_delay_ms":`...)
		dst = appendJSONInt64(dst, e.RetryDelayMS)
	}
	return append(dst, '}')
}

func appendOpenAPIEventJSON(dst []byte, e *OpenAPIEvent) []byte {
	dst = append(dst, `{"type":`...)
	dst = appendJSONString(dst, e.Type)
	dst = append(dst, `,"source_url":`...)
	dst = appendJSONString(dst, e.SourceURL)
	dst = append(dst, `,"endpoints":`...)
	dst = appendEndpointSliceJSON(dst, e.Endpoints)
	return append(dst, '}')
}

func appendSummaryEventJSON(dst []byte, e *SummaryEvent) ([]byte, error) {
	dst = append(dst, `{"type":`...)
	dst = appendJSONString(dst, e.Type)
	dst = append(dst, `,"seed":`...)
	dst = appendJSONString(dst, e.Seed)
	if e.Partial {
		dst = append(dst, `,"partial":`...)
		dst = appendJSONBool(dst, e.Partial)
	}
	dst = append(dst, `,"duration_ns":`...)
	dst = appendJSONInt64(dst, int64(e.Duration))
	dst = append(dst, `,"duration":`...)
	dst = appendJSONString(dst, e.DurationHuman)
	dst = append(dst, `,"urls_discovered_total":`...)
	dst = appendJSONInt64(dst, e.URLsDiscovered)
	dst = append(dst, `,"urls_unique":`...)
	dst = appendJSONInt64(dst, e.URLsUnique)
	dst = append(dst, `,"urls_in_scope_unique":`...)
	dst = appendJSONInt64(dst, e.URLsInScope)
	dst = append(dst, `,"endpoints_discovered":`...)
	dst = appendJSONInt64(dst, e.Endpoints)
	dst = append(dst, `,"params_discovered":`...)
	dst = appendJSONInt64(dst, e.Params)
	dst = append(dst, `,"forms_discovered":`...)
	dst = appendJSONInt64(dst, e.Forms)
	dst = append(dst, `,"js_files_discovered":`...)
	dst = appendJSONInt64(dst, e.JSFiles)
	dst = append(dst, `,"js_routes_discovered":`...)
	dst = appendJSONInt64(dst, e.JSRoutes)
	dst = append(dst, `,"requests_made":`...)
	dst = appendJSONInt64(dst, e.RequestsMade)
	dst = append(dst, `,"responses_ok":`...)
	dst = appendJSONInt64(dst, e.ResponsesOK)
	dst = append(dst, `,"responses_failed":`...)
	dst = appendJSONInt64(dst, e.ResponsesFailed)
	dst = append(dst, `,"redirects_followed":`...)
	dst = appendJSONInt64(dst, e.RedirectsFollowed)
	dst = append(dst, `,"duplicates_rejected":`...)
	dst = appendJSONInt64(dst, e.DuplicatesRejected)
	dst = append(dst, `,"robots_disallowed":`...)
	dst = appendJSONInt64(dst, e.RobotsDisallowed)
	dst = append(dst, `,"source_maps_recovered":`...)
	dst = appendJSONInt64(dst, e.SourceMapsRecovered)
	dst = append(dst, `,"openapi_endpoints_discovered":`...)
	dst = appendJSONInt64(dst, e.OpenAPIEndpoints)
	dst = append(dst, `,"retry_attempts":`...)
	dst = appendJSONInt64(dst, e.RetryAttempts)
	dst = append(dst, `,"retry_backoff_ns":`...)
	dst = appendJSONInt64(dst, int64(e.RetryBackoff))
	dst = append(dst, `,"active_wall_ns":`...)
	dst = appendJSONInt64(dst, int64(e.ActiveWall))
	dst = append(dst, `,"retry_backoff_ms":`...)
	dst = appendJSONInt64(dst, e.RetryBackoffMS)
	dst = append(dst, `,"active_wall_ms":`...)
	dst = appendJSONInt64(dst, e.ActiveWallMS)
	dst = append(dst, `,"urls_per_sec":`...)
	var err error
	dst, err = appendJSONFloat64(dst, e.URLsPerSec)
	if err != nil {
		return dst, err
	}
	dst = append(dst, `,"useful_unique_discoveries_per_sec":`...)
	dst, err = appendJSONFloat64(dst, e.UsefulDiscoveriesPerSec)
	if err != nil {
		return dst, err
	}
	dst = append(dst, `,"peak_memory_bytes":`...)
	dst = appendJSONUint64(dst, e.PeakMemoryBytes)
	dst = append(dst, '}')
	return dst, nil
}
