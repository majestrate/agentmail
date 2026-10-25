package directory

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// DecodeBase64 decodes a standard base64 value to a single line.
func DecodeBase64(name, raw string) (string, error) {
	stripped := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, raw)
	if stripped == "" {
		return "", fmt.Errorf("%s is empty", name)
	}
	buf, err := base64.StdEncoding.DecodeString(stripped)
	if err != nil {
		return "", fmt.Errorf("%s is not valid base64", name)
	}
	if strings.ContainsRune(string(buf), '\n') || strings.ContainsRune(string(buf), 0) {
		return "", fmt.Errorf("%s must decode to a single line", name)
	}
	return string(buf), nil
}

// EncodePageToken is the standard base64 form of a numeric cursor.
func EncodePageToken(id int64) string {
	if id <= 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

// DecodePageToken reads a cursor produced by EncodePageToken.
// An empty token means the first page.
func DecodePageToken(token string) (int64, error) {
	if strings.TrimSpace(token) == "" {
		return 0, nil
	}
	raw, err := DecodeBase64("page_token", token)
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("page_token is not valid base64")
	}
	return id, nil
}
