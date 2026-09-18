package util

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/aki237/nscjar"
	"github.com/govdbot/govd/internal/logger"
)

var cookiesCache = make(map[string][]*http.Cookie)

func GetExtractorCookies(extractorID string) []*http.Cookie {
	if extractorID == "" {
		return nil
	}
	cookieFile := extractorID + ".txt"
	return ParseCookieFile(cookieFile)
}

func ParseCookieFile(fileName string) []*http.Cookie {
	cachedCookies, ok := cookiesCache[fileName]
	if ok {
		return cachedCookies
	}

	cookiePath := filepath.Join("private/cookies", fileName)

	cookieFile, err := os.Open(cookiePath)
	if err != nil {
		return nil
	}
	defer cookieFile.Close()

	var parser nscjar.Parser
	cookies, err := parser.Unmarshal(cookieFile)
	if err != nil {
		logger.L.Warnf("failed parsing cookie file %s: %v", fileName, err)
		return nil
	}
	for _, cookie := range cookies {
		cookie.Value = unescapeCookieValue(cookie.Value)
	}
	cookiesCache[fileName] = cookies

	logger.L.Debugf("parsed cookie file: %s", fileName)
	return cookies
}

// unescapeCookieValue decodes the octal escapes used by the Netscape cookie
// format (e.g. `\054` -> `,`) and strips surrounding quotes. Without this,
// values such as Instagram's `rur` cookie keep raw backslashes, which
// net/http drops as invalid bytes and results in a rejected request.
func unescapeCookieValue(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	if !strings.ContainsRune(value, '\\') {
		return value
	}

	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+3 < len(value) {
			o1, o2, o3 := value[i+1], value[i+2], value[i+3]
			if isOctalDigit(o1) && isOctalDigit(o2) && isOctalDigit(o3) {
				b.WriteByte((o1-'0')*64 + (o2-'0')*8 + (o3 - '0'))
				i += 3
				continue
			}
		}
		b.WriteByte(value[i])
	}
	return b.String()
}

func isOctalDigit(b byte) bool {
	return b >= '0' && b <= '7'
}
