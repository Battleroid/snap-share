package accounts

import "strings"

// DeviceSummary turns a User-Agent into "Firefox · macOS" for the session list.
func DeviceSummary(ua string) string {
	if ua == "" {
		return "—"
	}
	l := strings.ToLower(ua)
	browser := "Unknown browser"
	switch {
	case strings.HasPrefix(l, "curl/"):
		return "curl"
	case strings.Contains(l, "edg/"):
		browser = "Edge"
	case strings.Contains(l, "firefox/"):
		browser = "Firefox"
	case strings.Contains(l, "opr/"):
		browser = "Opera"
	case strings.Contains(l, "chrome/"), strings.Contains(l, "crios/"):
		browser = "Chrome"
	case strings.Contains(l, "safari/"):
		browser = "Safari"
	}
	os := ""
	switch {
	case strings.Contains(l, "android"):
		os = "Android"
	case strings.Contains(l, "iphone"), strings.Contains(l, "ipad"):
		os = "iOS"
	case strings.Contains(l, "mac os x"), strings.Contains(l, "macintosh"):
		os = "macOS"
	case strings.Contains(l, "windows"):
		os = "Windows"
	case strings.Contains(l, "linux"):
		os = "Linux"
	}
	if os == "" {
		return browser
	}
	return browser + " · " + os
}
