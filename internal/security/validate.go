package security

import (
	"errors"
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	nodePattern      = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
	servicePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	rangePattern     = regexp.MustCompile(`^bytes=(\d*)-(\d*)$`)
)

func ValidateNodeName(value string) error {
	if value == "" || len(value) > 253 {
		return errors.New("invalid node name")
	}
	if !nodePattern.MatchString(value) {
		return errors.New("invalid node name")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) > 63 {
			return errors.New("invalid node name")
		}
	}
	return nil
}

func ValidateServiceID(value string) error {
	if !servicePattern.MatchString(value) {
		return errors.New("invalid service id")
	}
	return nil
}

func DecodePathSegment(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty path segment")
	}
	if strings.Contains(raw, "%00") || strings.Contains(raw, "%2f") || strings.Contains(raw, "%2F") ||
		strings.Contains(raw, "%5c") || strings.Contains(raw, "%5C") {
		return "", errors.New("encoded path separator is not allowed")
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", errors.New("invalid path encoding")
	}
	if strings.Contains(decoded, "%") {
		return "", errors.New("double encoding is not allowed")
	}
	return decoded, nil
}

func ValidateFileName(value string) error {
	if value == "" || value == "." || value == ".." {
		return errors.New("invalid filename")
	}
	if !utf8.ValidString(value) || len(value) > 255 {
		return errors.New("invalid filename")
	}
	if strings.ContainsAny(value, `/\`+"\x00") || strings.Contains(value, "%") {
		return errors.New("invalid filename")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return errors.New("invalid filename")
		}
	}
	return nil
}

func ValidateRequestID(value string) bool {
	return requestIDPattern.MatchString(value)
}

func ValidateRangeHeader(value string) error {
	if value == "" {
		return nil
	}
	match := rangePattern.FindStringSubmatch(value)
	if match == nil {
		return errors.New("invalid range header")
	}
	if match[1] == "" && match[2] == "" {
		return errors.New("invalid range header")
	}
	if match[1] != "" && match[2] != "" {
		start, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return errors.New("invalid range header")
		}
		end, err := strconv.ParseInt(match[2], 10, 64)
		if err != nil {
			return errors.New("invalid range header")
		}
		if start > end {
			return errors.New("invalid range header")
		}
	}
	return nil
}

func ContentDisposition(filename string) string {
	fallback := strings.Map(func(r rune) rune {
		switch {
		case r == '"' || r == '\'' || r == '\\' || r == '/' || r == '\r' || r == '\n':
			return '-'
		case r < 0x20 || r == 0x7f:
			return -1
		case r > 0x7e:
			return -1
		default:
			return r
		}
	}, filename)
	if fallback == "" {
		fallback = "download.log"
	}
	return mime.FormatMediaType("attachment", map[string]string{"filename": fallback})
}

func MustValidFileName(value string) string {
	if err := ValidateFileName(value); err != nil {
		panic(fmt.Sprintf("invalid trusted filename %q: %v", value, err))
	}
	return value
}
