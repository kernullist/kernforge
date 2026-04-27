package main

import (
	"os"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

func getSystemLocale() string {
	if lang := os.Getenv("LANG"); lang != "" {
		parts := strings.Split(lang, ".")
		return parts[0]
	}
	if runtime.GOOS == "windows" {
		kernel32 := syscall.NewLazyDLL("kernel32.dll")
		getUserDefaultLocaleName := kernel32.NewProc("GetUserDefaultLocaleName")
		buf := make([]uint16, 85)
		ret, _, _ := getUserDefaultLocaleName.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if ret != 0 {
			return syscall.UTF16ToString(buf)
		}
	}
	return "en-US"
}

func resolvedDisplayLocale(cfg Config) string {
	if configAutoLocale(cfg) {
		return strings.TrimSpace(getSystemLocale())
	}
	return "en-US"
}

func localePrefersKorean(cfg Config) bool {
	locale := strings.ToLower(strings.TrimSpace(resolvedDisplayLocale(cfg)))
	return strings.HasPrefix(locale, "ko")
}

func localizedText(cfg Config, english string, korean string) string {
	if localePrefersKorean(cfg) {
		return korean
	}
	return english
}

func responseLanguageInstructionForUserText(text string, cfg Config) string {
	language, reason := inferResponseLanguageForUserText(text, cfg)
	switch language {
	case "ko":
		if reason == "explicit" {
			return "Always respond in Korean because the latest user request explicitly asks for Korean. Keep code identifiers, paths, API names, and commands unchanged."
		}
		if reason == "question" {
			return "Respond in Korean because the latest user request is written in Korean. Keep code identifiers, paths, API names, and commands unchanged."
		}
		return "Respond in Korean because the configured/system locale prefers Korean. Keep code identifiers, paths, API names, and commands unchanged."
	case "en":
		if reason == "explicit" {
			return "Always respond in English because the latest user request explicitly asks for English."
		}
		if reason == "question" {
			return "Respond in English because the latest user request is written in English."
		}
		return "Respond in English because no clearer user-request language was detected."
	default:
		return ""
	}
}

func inferResponseLanguageForUserText(text string, cfg Config) (string, string) {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower != "" {
		switch {
		case containsAny(lower, "답변은 한국어", "한국어로 답", "한국어로 설명", "한글로 답", "한글로 설명", "korean only", "in korean", "respond in korean", "answer in korean"):
			return "ko", "explicit"
		case containsAny(lower, "답변은 영어", "영어로 답", "영어로 설명", "english only", "in english", "respond in english", "answer in english"):
			return "en", "explicit"
		}
	}
	if textContainsHangul(text) {
		return "ko", "question"
	}
	if textLooksMostlyEnglish(text) {
		return "en", "question"
	}
	if localePrefersKorean(cfg) {
		return "ko", "locale"
	}
	return "en", "fallback"
}

func configWithResponseLanguageForUserText(cfg Config, text string) Config {
	language, _ := inferResponseLanguageForUserText(text, cfg)
	switch language {
	case "ko":
		cfg.FuzzFuncOutputLanguage = "korean"
	case "en":
		cfg.FuzzFuncOutputLanguage = "english"
	}
	return cfg
}

func textContainsHangul(text string) bool {
	for _, r := range text {
		if r >= 0xAC00 && r <= 0xD7AF {
			return true
		}
		if r >= 0x1100 && r <= 0x11FF {
			return true
		}
		if r >= 0x3130 && r <= 0x318F {
			return true
		}
	}
	return false
}

func textLooksMostlyEnglish(text string) bool {
	letters := 0
	latin := 0
	for _, r := range text {
		switch {
		case r >= 'A' && r <= 'Z':
			letters++
			latin++
		case r >= 'a' && r <= 'z':
			letters++
			latin++
		case r >= 0x00C0 && r <= 0x024F:
			letters++
			latin++
		case (r >= 0x0400 && r <= 0x04FF) || (r >= 0x3040 && r <= 0x30FF) || (r >= 0x4E00 && r <= 0x9FFF):
			letters++
		}
	}
	return latin >= 3 && letters > 0 && latin*100/letters >= 70
}

func resolvedFunctionFuzzLocale(cfg Config) string {
	switch configFuzzFuncOutputLanguage(cfg) {
	case "english":
		return "en-US"
	case "korean":
		return "ko-KR"
	}
	return strings.TrimSpace(getSystemLocale())
}

func functionFuzzPrefersKorean(cfg Config) bool {
	locale := strings.ToLower(strings.TrimSpace(resolvedFunctionFuzzLocale(cfg)))
	return strings.HasPrefix(locale, "ko")
}

func functionFuzzLocalizedText(cfg Config, english string, korean string) string {
	if functionFuzzPrefersKorean(cfg) {
		return korean
	}
	return english
}
