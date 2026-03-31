//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	previewClassName    = "KernforgePreviewWindow"
	buttonClassName     = "BUTTON"
	wmCommand           = 0x0111
	bsPushButton        = 0x00000000
	idApplyButton       = 1001
	idCancelButton      = 1002
	buttonWidth         = 110
	buttonHeight        = 32
	buttonMargin        = 14
	previewWindowWidth  = 1220
	previewWindowHeight = 860
	emSetSel            = 0x00B1
	emScrollCaret       = 0x00B7
)

var (
	previewWndProcCallback  = syscall.NewCallback(previewWindowProc)
	previewEditorColor      = rgb(17, 24, 39)
	previewPrimaryTextColor = rgb(233, 239, 248)
	previewMetaTextColor    = rgb(143, 159, 181)
	previewDiffLinePattern  = regexp.MustCompile(`^([ +\-])\s*(\d+)\s\|\s?(.*)$`)
)

type previewState struct {
	title            string
	previewPath      string
	resultPath       string
	content          string
	displayed        string
	editHandle       uintptr
	applyButton      uintptr
	cancelButton     uintptr
	brandHandle      uintptr
	titleHandle      uintptr
	metaHandle       uintptr
	hintHandle       uintptr
	badgeHandle      uintptr
	statusHandle     uintptr
	codeFontHandle   uintptr
	titleFontHandle  uintptr
	metaFontHandle   uintptr
	hintFontHandle   uintptr
	badgeFontHandle  uintptr
	statusFontHandle uintptr
	buttonFontHandle uintptr
	backgroundBrush  uintptr
	editorBrush      uintptr
	iconBig          uintptr
	iconSmall        uintptr
}

var activePreview previewState

func runDiffPreviewProcess(previewPath, resultPath string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exePath, "-preview-file", previewPath, "-preview-result-file", resultPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func RunDiffPreviewWindow(previewPath, resultPath string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	activePreview = previewState{
		title:       "Kernforge Diff Preview",
		previewPath: previewPath,
		resultPath:  resultPath,
	}
	if data, err := os.ReadFile(previewPath); err == nil {
		activePreview.content = string(data)
		activePreview.displayed = formatPreviewContent(activePreview.content)
	}
	activePreview.iconBig, activePreview.iconSmall = loadViewerIcons()
	activePreview.backgroundBrush = createViewerBrush(viewerBackgroundColor)
	activePreview.editorBrush = createViewerBrush(previewEditorColor)
	msfteditDLL.Load()

	instance, _, _ := getModuleHandleWProc.Call(0)
	className, _ := syscall.UTF16PtrFromString(previewClassName)
	cursor, _, _ := loadCursorWProc.Call(0, uintptr(idcArrow))

	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   previewWndProcCallback,
		HInstance:     instance,
		HIcon:         activePreview.iconBig,
		HCursor:       cursor,
		HbrBackground: activePreview.backgroundBrush,
		LpszClassName: className,
		HIconSm:       activePreview.iconSmall,
	}
	registerClassExWProc.Call(uintptr(unsafe.Pointer(&wc)))

	titlePtr, _ := syscall.UTF16PtrFromString(activePreview.title)
	startX, startY := viewerWindowPosition(previewWindowWidth, previewWindowHeight)
	hwnd, _, err := createWindowExWProc.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(wsOverlappedWindow|wsVisible),
		int32ToUintptr(startX),
		int32ToUintptr(startY),
		previewWindowWidth,
		previewWindowHeight,
		0,
		0,
		instance,
		0,
	)
	if hwnd == 0 {
		return err
	}

	if activePreview.iconBig != 0 {
		sendMessageWProc.Call(hwnd, wmSetIcon, iconBig, activePreview.iconBig)
	}
	if activePreview.iconSmall != 0 {
		sendMessageWProc.Call(hwnd, wmSetIcon, iconSmall, activePreview.iconSmall)
	}

	showWindowProc.Call(hwnd, swShow)
	if activePreview.applyButton != 0 {
		setFocusProc.Call(activePreview.applyButton)
	}
	if activePreview.editHandle != 0 {
		sendMessageWProc.Call(activePreview.editHandle, emSetSel, 0, 0)
		hideCaretProc.Call(activePreview.editHandle)
	}

	var message msg
	for {
		result, _, _ := getMessageWProc.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		translateMessageProc.Call(uintptr(unsafe.Pointer(&message)))
		dispatchMessageWProc.Call(uintptr(unsafe.Pointer(&message)))
	}

	return nil
}

func previewWindowProc(hwnd uintptr, msgID uint32, wParam, lParam uintptr) uintptr {
	switch msgID {
	case wmCreate:
		createPreviewControls(hwnd)
		return 0
	case wmSize:
		layoutPreviewControls(hwnd)
		return 0
	case wmCommand:
		switch lowWord(wParam) {
		case idApplyButton:
			_ = os.WriteFile(activePreview.resultPath, []byte("apply"), 0o644)
			result, _, _ := defWindowProcWProc.Call(hwnd, uintptr(wmClose), 0, 0)
			return result
		case idCancelButton:
			_ = os.WriteFile(activePreview.resultPath, []byte("cancel"), 0o644)
			result, _, _ := defWindowProcWProc.Call(hwnd, uintptr(wmClose), 0, 0)
			return result
		}
	case wmCtlColorEdit, wmCtlColorStatic:
		if brush := previewControlBrush(wParam, lParam); brush != 0 {
			return brush
		}
	case wmClose:
		if activePreview.resultPath != "" {
			if _, err := os.Stat(activePreview.resultPath); os.IsNotExist(err) {
				_ = os.WriteFile(activePreview.resultPath, []byte("cancel"), 0o644)
			}
		}
		result, _, _ := defWindowProcWProc.Call(hwnd, uintptr(msgID), wParam, lParam)
		return result
	case wmDestroy:
		destroyPreviewResources()
		postQuitMessageProc.Call(0)
		return 0
	}
	result, _, _ := defWindowProcWProc.Call(hwnd, uintptr(msgID), wParam, lParam)
	return result
}

func createPreviewControls(parent uintptr) {
	instance, _, _ := getModuleHandleWProc.Call(0)
	editClassPtr, _ := syscall.UTF16PtrFromString("EDIT")
	buttonClassPtr, _ := syscall.UTF16PtrFromString(buttonClassName)
	emptyPtr, _ := syscall.UTF16PtrFromString("")
	applyPtr, _ := syscall.UTF16PtrFromString("Apply")
	cancelPtr, _ := syscall.UTF16PtrFromString("Cancel")

	activePreview.brandHandle = createViewerStatic(parent, "KERNFORGE REVIEW", 0)
	activePreview.titleHandle = createViewerStatic(parent, previewTitleText(), 0)
	activePreview.metaHandle = createViewerStatic(parent, previewMetaSummary(), 0)
	activePreview.hintHandle = createViewerStatic(parent, "Review the proposed edit before applying it to your workspace.", 0)
	activePreview.badgeHandle = createViewerStatic(parent, "READ ONLY", ssRight)
	activePreview.statusHandle = createViewerStatic(parent, "Approve to write the change, or cancel to keep the file unchanged.", 0)

	activePreview.editHandle, _, _ = createWindowExWProc.Call(
		wsExClientEdge,
		uintptr(unsafe.Pointer(editClassPtr)),
		uintptr(unsafe.Pointer(emptyPtr)),
		uintptr(wsChild|wsVisible|wsVScroll|wsHScroll|esMultiline|esAutoVScroll|esAutoHScroll|esReadOnly),
		0,
		0,
		100,
		100,
		parent,
		0,
		instance,
		0,
	)

	activePreview.applyButton, _, _ = createWindowExWProc.Call(
		0,
		uintptr(unsafe.Pointer(buttonClassPtr)),
		uintptr(unsafe.Pointer(applyPtr)),
		uintptr(wsChild|wsVisible|bsPushButton),
		0,
		0,
		buttonWidth,
		buttonHeight,
		parent,
		uintptr(idApplyButton),
		instance,
		0,
	)

	activePreview.cancelButton, _, _ = createWindowExWProc.Call(
		0,
		uintptr(unsafe.Pointer(buttonClassPtr)),
		uintptr(unsafe.Pointer(cancelPtr)),
		uintptr(wsChild|wsVisible|bsPushButton),
		0,
		0,
		buttonWidth,
		buttonHeight,
		parent,
		uintptr(idCancelButton),
		instance,
		0,
	)

	activePreview.codeFontHandle = createViewerFont("Consolas", -18, fwNormal, fixedPitchFFModern)
	activePreview.titleFontHandle = createViewerFont("Segoe UI", -26, fwSemiBold, variablePitchSwiss)
	activePreview.metaFontHandle = createViewerFont("Segoe UI", -15, fwMedium, variablePitchSwiss)
	activePreview.hintFontHandle = createViewerFont("Segoe UI", -15, fwNormal, variablePitchSwiss)
	activePreview.badgeFontHandle = createViewerFont("Segoe UI", -14, fwSemiBold, variablePitchSwiss)
	activePreview.statusFontHandle = createViewerFont("Segoe UI", -15, fwMedium, variablePitchSwiss)
	activePreview.buttonFontHandle = createViewerFont("Segoe UI", -14, fwMedium, variablePitchSwiss)

	applyViewerFont(activePreview.editHandle, activePreview.codeFontHandle)
	applyViewerFont(activePreview.titleHandle, activePreview.titleFontHandle)
	applyViewerFont(activePreview.metaHandle, activePreview.metaFontHandle)
	applyViewerFont(activePreview.hintHandle, activePreview.hintFontHandle)
	applyViewerFont(activePreview.brandHandle, activePreview.badgeFontHandle)
	applyViewerFont(activePreview.badgeHandle, activePreview.badgeFontHandle)
	applyViewerFont(activePreview.statusHandle, activePreview.statusFontHandle)
	applyViewerFont(activePreview.applyButton, activePreview.buttonFontHandle)
	applyViewerFont(activePreview.cancelButton, activePreview.buttonFontHandle)

	if activePreview.editHandle != 0 {
		textPtr, _ := syscall.UTF16PtrFromString(activePreview.displayed)
		sendMessageWProc.Call(activePreview.editHandle, wmSetText, 0, uintptr(unsafe.Pointer(textPtr)))
	}

	layoutPreviewControls(parent)
	if activePreview.applyButton != 0 {
		setFocusProc.Call(activePreview.applyButton)
	}
	if activePreview.editHandle != 0 {
		hideCaretProc.Call(activePreview.editHandle)
	}
}

func layoutPreviewControls(parent uintptr) {
	var rc rect
	getClientRectProc.Call(parent, uintptr(unsafe.Pointer(&rc)))
	width := rc.Right - rc.Left
	height := rc.Bottom - rc.Top

	padding := int32(24)
	brandY := int32(18)
	brandH := int32(18)
	badgeW := int32(120)
	badgeH := int32(18)
	titleY := brandY + 22
	titleH := int32(36)
	metaY := titleY + 34
	metaH := int32(20)
	hintY := metaY + 24
	hintH := int32(20)
	contentY := hintY + hintH + 18

	btnW := int32(buttonWidth)
	btnH := int32(buttonHeight)
	statusH := btnH
	statusY := height - padding - statusH
	contentH := statusY - contentY - 16
	if contentH < 120 {
		contentH = 120
	}

	cancelX := width - padding - btnW
	applyX := cancelX - 10 - btnW
	statusW := applyX - padding - 10
	if statusW < 0 {
		statusW = 0
	}

	moveWindowProc.Call(activePreview.brandHandle, uintptr(padding), uintptr(brandY), uintptr(width-padding*2-badgeW-12), uintptr(brandH), 1)
	moveWindowProc.Call(activePreview.badgeHandle, uintptr(width-padding-badgeW), uintptr(brandY), uintptr(badgeW), uintptr(badgeH), 1)
	moveWindowProc.Call(activePreview.titleHandle, uintptr(padding), uintptr(titleY), uintptr(width-padding*2), uintptr(titleH), 1)
	moveWindowProc.Call(activePreview.metaHandle, uintptr(padding), uintptr(metaY), uintptr(width-padding*2), uintptr(metaH), 1)
	moveWindowProc.Call(activePreview.hintHandle, uintptr(padding), uintptr(hintY), uintptr(width-padding*2), uintptr(hintH), 1)
	moveWindowProc.Call(activePreview.editHandle, uintptr(padding), uintptr(contentY), uintptr(width-padding*2), uintptr(contentH), 1)
	moveWindowProc.Call(activePreview.statusHandle, uintptr(padding), uintptr(statusY), uintptr(statusW), uintptr(statusH), 1)
	moveWindowProc.Call(activePreview.applyButton, uintptr(applyX), uintptr(statusY), uintptr(btnW), uintptr(btnH), 1)
	moveWindowProc.Call(activePreview.cancelButton, uintptr(cancelX), uintptr(statusY), uintptr(btnW), uintptr(btnH), 1)
}

func lowWord(v uintptr) uintptr {
	return v & 0xFFFF
}

func previewTitleText() string {
	title := activePreview.title
	lines := strings.Split(activePreview.content, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		title = strings.TrimSpace(lines[0])
	}
	return title
}

func previewMetaText() string {
	fileName := filepath.Base(activePreview.previewPath)
	lineCount := strings.Count(activePreview.displayed, "\n")
	if strings.TrimSpace(activePreview.displayed) != "" {
		lineCount++
	}
	return filepath.ToSlash(fileName) + "  •  " + pluralizePreviewLines(lineCount) + "  •  Diff review"
}

func pluralizePreviewLines(count int) string {
	if count == 1 {
		return "1 line"
	}
	return strconv.Itoa(count) + " lines"
}

func previewMetaSummary() string {
	fileName := filepath.Base(activePreview.previewPath)
	lineCount := strings.Count(activePreview.displayed, "\n")
	if strings.TrimSpace(activePreview.displayed) != "" {
		lineCount++
	}
	return filepath.ToSlash(fileName) + "  -  " + pluralizePreviewLines(lineCount) + "  -  Diff review"
}

func previewControlBrush(hdc, control uintptr) uintptr {
	if control == 0 {
		return 0
	}
	if control == activePreview.editHandle {
		setBkColorProc.Call(hdc, previewEditorColor)
		setTextColorProc.Call(hdc, previewPrimaryTextColor)
		if activePreview.editorBrush != 0 {
			return activePreview.editorBrush
		}
		return activePreview.backgroundBrush
	}
	if !isPreviewLabel(control) || activePreview.backgroundBrush == 0 {
		return 0
	}
	setBkModeProc.Call(hdc, transparentBkMode)
	setTextColorProc.Call(hdc, previewLabelColor(control))
	return activePreview.backgroundBrush
}

func isPreviewLabel(handle uintptr) bool {
	switch handle {
	case activePreview.brandHandle, activePreview.titleHandle, activePreview.metaHandle, activePreview.hintHandle, activePreview.badgeHandle, activePreview.statusHandle:
		return true
	default:
		return false
	}
}

func previewLabelColor(handle uintptr) uintptr {
	switch handle {
	case activePreview.brandHandle:
		return viewerAccentTextColor
	case activePreview.titleHandle:
		return viewerStrongTextColor
	case activePreview.metaHandle, activePreview.hintHandle, activePreview.statusHandle:
		return previewMetaTextColor
	case activePreview.badgeHandle:
		return viewerPrimaryTextColor
	default:
		return viewerPrimaryTextColor
	}
}

func destroyPreviewResources() {
	for _, handle := range []uintptr{
		activePreview.codeFontHandle,
		activePreview.titleFontHandle,
		activePreview.metaFontHandle,
		activePreview.hintFontHandle,
		activePreview.badgeFontHandle,
		activePreview.statusFontHandle,
		activePreview.buttonFontHandle,
		activePreview.backgroundBrush,
		activePreview.editorBrush,
	} {
		if handle != 0 {
			deleteObjectProc.Call(handle)
		}
	}
	activePreview = previewState{}
}

func applyPreviewEditTheme() {
	if activePreview.editHandle == 0 {
		return
	}
	sendMessageWProc.Call(activePreview.editHandle, emSetSel, 0, ^uintptr(0))
	applyPreviewSelectionColor(previewPrimaryTextColor)
	sendMessageWProc.Call(activePreview.editHandle, emSetSel, 0, 0)
	sendMessageWProc.Call(activePreview.editHandle, emScrollCaret, 0, 0)
}

func formatPreviewContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSpace(content)
	if content == "" {
		return "No preview available."
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "Write ") {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}

	var formatted []string
	for i, line := range lines {
		switch {
		case i == 0 && strings.HasPrefix(line, "Preview for "):
			formatted = append(formatted, strings.TrimSpace(line))
			formatted = append(formatted, strings.Repeat("-", 56))
		case strings.HasPrefix(line, "--- before/"):
			formatted = append(formatted, "FROM  "+strings.TrimPrefix(line, "--- "))
		case strings.HasPrefix(line, "+++ after/"):
			formatted = append(formatted, "TO    "+strings.TrimPrefix(line, "+++ "))
			formatted = append(formatted, strings.Repeat("-", 56))
		default:
			formatted = append(formatted, formatPreviewBodyLine(line))
		}
	}
	return strings.Join(formatted, "\r\n")
}

func formatPreviewBodyLine(line string) string {
	matches := previewDiffLinePattern.FindStringSubmatch(line)
	if len(matches) != 4 {
		return line
	}

	label := "CTX"
	switch matches[1] {
	case "+":
		label = "ADD"
	case "-":
		label = "DEL"
	}

	lineNo, err := strconv.Atoi(matches[2])
	if err != nil {
		return line
	}

	body := matches[3]
	return formatPreviewGutterLine(label, lineNo, body)
}

func formatPreviewGutterLine(label string, lineNo int, body string) string {
	return label + " " + fmt.Sprintf("%4d | %s", lineNo, body)
}

func applyPreviewSelectionColor(color uintptr) {
	if activePreview.editHandle == 0 {
		return
	}
	format := charFormat{
		CbSize:      uint32(unsafe.Sizeof(charFormat{})),
		DwMask:      cfmColor,
		CrTextColor: uint32(color),
	}
	sendMessageWProc.Call(activePreview.editHandle, emSetCharFormat, scfAll, uintptr(unsafe.Pointer(&format)))
}
