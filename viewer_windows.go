//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

const (
	viewerClassName    = "KernforgeViewerWindow"
	msfteditClassName  = "RICHEDIT50W"
	staticClassName    = "STATIC"
	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000
	wsChild            = 0x40000000
	wsVScroll          = 0x00200000
	wsHScroll          = 0x00100000
	wsExClientEdge     = 0x00000200
	esMultiline        = 0x0004
	esAutoVScroll      = 0x0040
	esAutoHScroll      = 0x0080
	esNoHideSel        = 0x0100
	esReadOnly         = 0x0800
	ssRight            = 0x00000002
	wmCreate           = 0x0001
	wmDestroy          = 0x0002
	wmSize             = 0x0005
	wmClose            = 0x0010
	wmNotify           = 0x004E
	wmSetIcon          = 0x0080
	wmSetFont          = 0x0030
	wmSetText          = 0x000C
	wmCtlColorEdit     = 0x0133
	wmCtlColorStatic   = 0x0138
	emExGetSel         = 0x0434
	emSetBkgndColor    = 0x0443
	emSetCharFormat    = 0x0444
	emSetEventMask     = 0x0445
	emSetReadOnly      = 0x00CF
	emUndo             = 0x00C7
	wmGettextlength    = 0x000E
	wmGettext          = 0x000D
	enmSelChange       = 0x00080000
	enSelChange        = 0x0702
	scfAll             = 0x0004
	scfDefault         = 0x0000
	cfmColor           = 0x40000000
	swShow             = 5
	swHide             = 0
	colorWindow        = 5
	idcArrow           = 32512
	imageIcon          = 1
	iconSmall          = 0
	iconBig            = 1
	smCxScreen         = 0
	smCyScreen         = 1
	fwNormal           = 400
	fwMedium           = 500
	fwSemiBold         = 600
	defaultCharset     = 1
	outDefaultPrecis   = 0
	clipDefaultPrecis  = 0
	cleartypeQuality   = 5
	fixedPitchFFModern = 49
	variablePitchSwiss = 34
	transparentBkMode  = 1
	lrLoadFromFile     = 0x0010
	lrDefaultSize      = 0x0040

	viewerWindowWidth  = 1220
	viewerWindowHeight = 860

	idEditButton      = 2001
	idSaveButton      = 2002
	idUndoButton      = 2003
	viewerButtonWidth = 84
	viewerButtonHeight = 26
	viewerButtonGap   = 8
)

type fileEncoding int

const (
	encodingUTF8    fileEncoding = iota
	encodingUTF8BOM
	encodingUTF16LE
	encodingUTF16BE
	encodingANSI // CP_ACP (e.g. EUC-KR/CP949 on Korean Windows)
)

var (
	viewerBackgroundColor  = rgb(10, 15, 23)
	viewerEditorColor      = rgb(18, 27, 41)
	viewerPrimaryTextColor = rgb(238, 243, 252)
	viewerMutedTextColor   = rgb(143, 159, 181)
	viewerAccentTextColor  = rgb(245, 166, 76)
	viewerStrongTextColor  = rgb(255, 220, 174)

	user32ViewerDLL       = syscall.NewLazyDLL("user32.dll")
	gdi32ViewerDLL        = syscall.NewLazyDLL("gdi32.dll")
	kernel32ViewerDLL     = syscall.NewLazyDLL("kernel32.dll")
	msfteditDLL           = syscall.NewLazyDLL("Msftedit.dll")
	registerClassExWProc  = user32ViewerDLL.NewProc("RegisterClassExW")
	createWindowExWProc   = user32ViewerDLL.NewProc("CreateWindowExW")
	defWindowProcWProc    = user32ViewerDLL.NewProc("DefWindowProcW")
	dispatchMessageWProc  = user32ViewerDLL.NewProc("DispatchMessageW")
	getClientRectProc     = user32ViewerDLL.NewProc("GetClientRect")
	getMessageWProc       = user32ViewerDLL.NewProc("GetMessageW")
	getSystemMetricsProc  = user32ViewerDLL.NewProc("GetSystemMetrics")
	loadCursorWProc       = user32ViewerDLL.NewProc("LoadCursorW")
	loadImageWProc        = user32ViewerDLL.NewProc("LoadImageW")
	moveWindowProc        = user32ViewerDLL.NewProc("MoveWindow")
	postQuitMessageProc   = user32ViewerDLL.NewProc("PostQuitMessage")
	sendMessageWProc      = user32ViewerDLL.NewProc("SendMessageW")
	setFocusProc          = user32ViewerDLL.NewProc("SetFocus")
	showWindowProc        = user32ViewerDLL.NewProc("ShowWindow")
	translateMessageProc  = user32ViewerDLL.NewProc("TranslateMessage")
	hideCaretProc         = user32ViewerDLL.NewProc("HideCaret")
	createFontWProc       = gdi32ViewerDLL.NewProc("CreateFontW")
	createSolidBrushProc  = gdi32ViewerDLL.NewProc("CreateSolidBrush")
	deleteObjectProc      = gdi32ViewerDLL.NewProc("DeleteObject")
	setBkColorProc        = gdi32ViewerDLL.NewProc("SetBkColor")
	setBkModeProc         = gdi32ViewerDLL.NewProc("SetBkMode")
	setTextColorProc      = gdi32ViewerDLL.NewProc("SetTextColor")
	getModuleHandleWProc      = kernel32ViewerDLL.NewProc("GetModuleHandleW")
	multiByteToWideCharProc   = kernel32ViewerDLL.NewProc("MultiByteToWideChar")
	wideCharToMultiByteProc   = kernel32ViewerDLL.NewProc("WideCharToMultiByte")
	viewerWndProcCallback     = syscall.NewCallback(viewerWindowProc)
)

type point struct {
	X int32
	Y int32
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type msg struct {
	HWnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type charRange struct {
	Min int32
	Max int32
}

type charFormat struct {
	CbSize          uint32
	DwMask          uint32
	DwEffects       uint32
	YHeight         int32
	YOffset         int32
	CrTextColor     uint32
	BCharSet        byte
	BPitchAndFamily byte
	SzFaceName      [32]uint16
}

type nmhdr struct {
	HWndFrom uintptr
	IDFrom   uintptr
	Code     uint32
	_        uint32
}

type viewerState struct {
	hwnd       uintptr
	title      string
	filePath   string
	resultPath string
	displayed  string
	rawContent string
	lineCount  int
	encoding   fileEncoding
	useCRLF    bool
	editMode   bool

	brandHandle  uintptr
	titleHandle  uintptr
	metaHandle   uintptr
	hintHandle   uintptr
	badgeHandle  uintptr
	statusHandle uintptr
	editHandle   uintptr
	editButton   uintptr
	saveButton   uintptr
	undoButton   uintptr

	codeFontHandle   uintptr
	titleFontHandle  uintptr
	metaFontHandle   uintptr
	hintFontHandle   uintptr
	badgeFontHandle  uintptr
	statusFontHandle uintptr
	buttonFontHandle uintptr

	backgroundBrush uintptr
	editorBrush     uintptr
	iconBig         uintptr
	iconSmall       uintptr
}

var activeViewer viewerState

func OpenTextViewer(path string, data []byte, resultPath string) error {
	_ = data
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exePath, "-viewer-file", path, "-viewer-result-file", resultPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func RunTextViewerWindow(path string, resultPath string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resultPath) != "" {
		_ = os.Remove(resultPath)
	}

	enc := detectFileEncoding(data)
	rawContent := decodeFileContent(data, enc)
	useCRLF := strings.Contains(rawContent, "\r\n")

	activeViewer = viewerState{
		title:      fmt.Sprintf("Kernforge Open - %s", filepath.Base(path)),
		filePath:   path,
		resultPath: resultPath,
		rawContent: rawContent,
		displayed:  formatViewerText(rawContent),
		lineCount:  viewerLineCount(rawContent),
		encoding:   enc,
		useCRLF:    useCRLF,
	}
	activeViewer.iconBig, activeViewer.iconSmall = loadViewerIcons()
	activeViewer.backgroundBrush = createViewerBrush(viewerBackgroundColor)
	activeViewer.editorBrush = createViewerBrush(viewerEditorColor)

	msfteditDLL.Load()

	instance, _, _ := getModuleHandleWProc.Call(0)
	className, _ := syscall.UTF16PtrFromString(viewerClassName)
	cursor, _, _ := loadCursorWProc.Call(0, uintptr(idcArrow))

	backgroundBrush := activeViewer.backgroundBrush
	if backgroundBrush == 0 {
		backgroundBrush = uintptr(colorWindow + 1)
	}
	wc := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   viewerWndProcCallback,
		HInstance:     instance,
		HIcon:         activeViewer.iconBig,
		HCursor:       cursor,
		HbrBackground: backgroundBrush,
		LpszClassName: className,
		HIconSm:       activeViewer.iconSmall,
	}
	registerClassExWProc.Call(uintptr(unsafe.Pointer(&wc)))

	titlePtr, _ := syscall.UTF16PtrFromString(activeViewer.title)
	startX, startY := viewerWindowPosition(viewerWindowWidth, viewerWindowHeight)
	hwnd, _, createErr := createWindowExWProc.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(wsOverlappedWindow|wsVisible),
		int32ToUintptr(startX),
		int32ToUintptr(startY),
		viewerWindowWidth,
		viewerWindowHeight,
		0,
		0,
		instance,
		0,
	)
	if hwnd == 0 {
		return createErr
	}

	if activeViewer.iconBig != 0 {
		sendMessageWProc.Call(hwnd, wmSetIcon, iconBig, activeViewer.iconBig)
	}
	if activeViewer.iconSmall != 0 {
		sendMessageWProc.Call(hwnd, wmSetIcon, iconSmall, activeViewer.iconSmall)
	}

	showWindowProc.Call(hwnd, swShow)

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

func viewerWindowProc(hwnd uintptr, msgID uint32, wParam, lParam uintptr) uintptr {
	switch msgID {
	case wmCreate:
		createViewerControls(hwnd)
		return 0
	case wmSize:
		layoutViewerControls(hwnd)
		return 0
	case wmCommand:
		switch lowWord(wParam) {
		case idEditButton:
			if activeViewer.editMode {
				exitViewerEditMode()
			} else {
				enterViewerEditMode()
			}
			return 0
		case idSaveButton:
			if err := saveViewerFile(); err != nil {
				sendViewerText(activeViewer.statusHandle, "Save failed: "+err.Error())
			} else {
				sendViewerText(activeViewer.statusHandle, "Saved successfully.")
			}
			return 0
		case idUndoButton:
			sendMessageWProc.Call(activeViewer.editHandle, emUndo, 0, 0)
			return 0
		}
	case wmNotify:
		if handleViewerNotify(lParam) {
			return 0
		}
	case wmCtlColorEdit, wmCtlColorStatic:
		if brush := viewerControlBrush(wParam, lParam); brush != 0 {
			return brush
		}
	case wmClose:
		_ = writeViewerSelectionResult()
		result, _, _ := defWindowProcWProc.Call(hwnd, uintptr(msgID), wParam, lParam)
		return result
	case wmDestroy:
		destroyViewerResources()
		postQuitMessageProc.Call(0)
		return 0
	}
	result, _, _ := defWindowProcWProc.Call(hwnd, uintptr(msgID), wParam, lParam)
	return result
}

func createViewerControls(parent uintptr) {
	activeViewer.hwnd = parent
	activeViewer.brandHandle = createViewerStatic(parent, "KERNFORGE OPEN", 0)
	activeViewer.titleHandle = createViewerStatic(parent, filepath.Base(activeViewer.filePath), 0)
	activeViewer.metaHandle = createViewerStatic(parent, viewerMetaText(activeViewer.filePath, activeViewer.lineCount), 0)
	activeViewer.hintHandle = createViewerStatic(parent, viewerHintText(), 0)
	activeViewer.badgeHandle = createViewerStatic(parent, "READ ONLY", ssRight)
	activeViewer.statusHandle = createViewerStatic(parent, "", 0)
	activeViewer.editHandle = createViewerRichEdit(parent)

	activeViewer.editButton = createViewerButton(parent, "Edit", idEditButton, true)
	activeViewer.saveButton = createViewerButton(parent, "Save", idSaveButton, false)
	activeViewer.undoButton = createViewerButton(parent, "Undo", idUndoButton, false)

	activeViewer.codeFontHandle = createViewerFont("Consolas", -18, fwNormal, fixedPitchFFModern)
	activeViewer.titleFontHandle = createViewerFont("Segoe UI", -26, fwSemiBold, variablePitchSwiss)
	activeViewer.metaFontHandle = createViewerFont("Segoe UI", -15, fwMedium, variablePitchSwiss)
	activeViewer.hintFontHandle = createViewerFont("Segoe UI", -15, fwNormal, variablePitchSwiss)
	activeViewer.badgeFontHandle = createViewerFont("Segoe UI", -14, fwSemiBold, variablePitchSwiss)
	activeViewer.statusFontHandle = createViewerFont("Segoe UI", -15, fwMedium, variablePitchSwiss)
	activeViewer.buttonFontHandle = createViewerFont("Segoe UI", -14, fwMedium, variablePitchSwiss)

	applyViewerFont(activeViewer.editHandle, activeViewer.codeFontHandle)
	applyViewerFont(activeViewer.titleHandle, activeViewer.titleFontHandle)
	applyViewerFont(activeViewer.metaHandle, activeViewer.metaFontHandle)
	applyViewerFont(activeViewer.hintHandle, activeViewer.hintFontHandle)
	applyViewerFont(activeViewer.brandHandle, activeViewer.badgeFontHandle)
	applyViewerFont(activeViewer.badgeHandle, activeViewer.badgeFontHandle)
	applyViewerFont(activeViewer.statusHandle, activeViewer.statusFontHandle)
	applyViewerFont(activeViewer.editButton, activeViewer.buttonFontHandle)
	applyViewerFont(activeViewer.saveButton, activeViewer.buttonFontHandle)
	applyViewerFont(activeViewer.undoButton, activeViewer.buttonFontHandle)

	if activeViewer.editHandle != 0 {
		sendMessageWProc.Call(activeViewer.editHandle, emSetBkgndColor, 0, viewerEditorColor)
		textPtr, _ := syscall.UTF16PtrFromString(activeViewer.displayed)
		sendMessageWProc.Call(activeViewer.editHandle, wmSetText, 0, uintptr(unsafe.Pointer(textPtr)))
		applyViewerEditTheme()
		sendMessageWProc.Call(activeViewer.editHandle, emSetEventMask, 0, enmSelChange)
	}

	updateViewerStatus()
	layoutViewerControls(parent)
}

func createViewerRichEdit(parent uintptr) uintptr {
	instance, _, _ := getModuleHandleWProc.Call(0)
	classPtr, _ := syscall.UTF16PtrFromString(msfteditClassName)
	emptyPtr, _ := syscall.UTF16PtrFromString("")
	handle, _, _ := createWindowExWProc.Call(
		0,
		uintptr(unsafe.Pointer(classPtr)),
		uintptr(unsafe.Pointer(emptyPtr)),
		uintptr(wsChild|wsVisible|wsVScroll|wsHScroll|esMultiline|esAutoVScroll|esAutoHScroll|esNoHideSel|esReadOnly),
		0,
		0,
		100,
		100,
		parent,
		0,
		instance,
		0,
	)
	return handle
}

func createViewerButton(parent uintptr, text string, id uintptr, visible bool) uintptr {
	instance, _, _ := getModuleHandleWProc.Call(0)
	classPtr, _ := syscall.UTF16PtrFromString(buttonClassName)
	textPtr, _ := syscall.UTF16PtrFromString(text)
	style := uintptr(wsChild | bsPushButton)
	if visible {
		style |= wsVisible
	}
	handle, _, _ := createWindowExWProc.Call(
		0,
		uintptr(unsafe.Pointer(classPtr)),
		uintptr(unsafe.Pointer(textPtr)),
		style,
		0, 0,
		uintptr(viewerButtonWidth),
		uintptr(viewerButtonHeight),
		parent,
		id,
		instance,
		0,
	)
	return handle
}

func createViewerStatic(parent uintptr, text string, style uintptr) uintptr {
	instance, _, _ := getModuleHandleWProc.Call(0)
	classPtr, _ := syscall.UTF16PtrFromString(staticClassName)
	textPtr, _ := syscall.UTF16PtrFromString(text)
	handle, _, _ := createWindowExWProc.Call(
		0,
		uintptr(unsafe.Pointer(classPtr)),
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(wsChild|wsVisible)|style,
		0,
		0,
		100,
		24,
		parent,
		0,
		instance,
		0,
	)
	return handle
}

func createViewerFont(face string, height int32, weight int, family uintptr) uintptr {
	fontName, err := syscall.UTF16PtrFromString(face)
	if err != nil {
		return 0
	}
	handle, _, _ := createFontWProc.Call(
		int32ToUintptr(height),
		0,
		0,
		0,
		uintptr(weight),
		0,
		0,
		0,
		defaultCharset,
		outDefaultPrecis,
		clipDefaultPrecis,
		cleartypeQuality,
		family,
		uintptr(unsafe.Pointer(fontName)),
	)
	return handle
}

func applyViewerFont(handle, font uintptr) {
	if handle != 0 && font != 0 {
		sendMessageWProc.Call(handle, wmSetFont, font, 1)
	}
}

func applyViewerEditTheme() {
	if activeViewer.editHandle == 0 {
		return
	}
	format := charFormat{
		CbSize:      uint32(unsafe.Sizeof(charFormat{})),
		DwMask:      cfmColor,
		CrTextColor: uint32(viewerPrimaryTextColor),
	}
	sendMessageWProc.Call(activeViewer.editHandle, emSetCharFormat, scfDefault, uintptr(unsafe.Pointer(&format)))
	sendMessageWProc.Call(activeViewer.editHandle, emSetCharFormat, scfAll, uintptr(unsafe.Pointer(&format)))
}

func layoutViewerControls(parent uintptr) {
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

	btnW := int32(viewerButtonWidth)
	btnH := int32(viewerButtonHeight)
	btnGap := int32(viewerButtonGap)

	statusH := btnH
	statusY := height - padding - statusH
	contentH := statusY - contentY - 16
	if contentH < 120 {
		contentH = 120
	}

	editBtnX := width - padding - btnW

	var statusW int32
	if activeViewer.editMode {
		saveBtnX := editBtnX - btnGap - btnW
		undoBtnX := saveBtnX - btnGap - btnW
		statusW = undoBtnX - padding - btnGap
		moveWindowProc.Call(activeViewer.saveButton, uintptr(saveBtnX), uintptr(statusY), uintptr(btnW), uintptr(btnH), 1)
		moveWindowProc.Call(activeViewer.undoButton, uintptr(undoBtnX), uintptr(statusY), uintptr(btnW), uintptr(btnH), 1)
	} else {
		statusW = editBtnX - padding - btnGap
	}
	if statusW < 0 {
		statusW = 0
	}

	moveWindowProc.Call(activeViewer.brandHandle, uintptr(padding), uintptr(brandY), uintptr(width-padding*2-badgeW-12), uintptr(brandH), 1)
	moveWindowProc.Call(activeViewer.badgeHandle, uintptr(width-padding-badgeW), uintptr(brandY), uintptr(badgeW), uintptr(badgeH), 1)
	moveWindowProc.Call(activeViewer.titleHandle, uintptr(padding), uintptr(titleY), uintptr(width-padding*2), uintptr(titleH), 1)
	moveWindowProc.Call(activeViewer.metaHandle, uintptr(padding), uintptr(metaY), uintptr(width-padding*2), uintptr(metaH), 1)
	moveWindowProc.Call(activeViewer.hintHandle, uintptr(padding), uintptr(hintY), uintptr(width-padding*2), uintptr(hintH), 1)
	moveWindowProc.Call(activeViewer.editHandle, uintptr(padding), uintptr(contentY), uintptr(width-padding*2), uintptr(contentH), 1)
	moveWindowProc.Call(activeViewer.statusHandle, uintptr(padding), uintptr(statusY), uintptr(statusW), uintptr(statusH), 1)
	moveWindowProc.Call(activeViewer.editButton, uintptr(editBtnX), uintptr(statusY), uintptr(btnW), uintptr(btnH), 1)
}

func handleViewerNotify(lParam uintptr) bool {
	if lParam == 0 {
		return false
	}
	header := (*nmhdr)(unsafe.Pointer(lParam))
	if header.HWndFrom != activeViewer.editHandle || header.Code != enSelChange {
		return false
	}
	updateViewerStatus()
	return true
}

func viewerControlBrush(hdc, control uintptr) uintptr {
	if control == 0 {
		return 0
	}
	if control == activeViewer.editHandle {
		setBkColorProc.Call(hdc, viewerEditorColor)
		setTextColorProc.Call(hdc, viewerPrimaryTextColor)
		if activeViewer.editorBrush != 0 {
			return activeViewer.editorBrush
		}
		return activeViewer.backgroundBrush
	}
	if !isViewerLabel(control) || activeViewer.backgroundBrush == 0 {
		return 0
	}
	setBkModeProc.Call(hdc, transparentBkMode)
	setTextColorProc.Call(hdc, viewerLabelColor(control))
	return activeViewer.backgroundBrush
}

func isViewerLabel(handle uintptr) bool {
	switch handle {
	case activeViewer.brandHandle, activeViewer.titleHandle, activeViewer.metaHandle, activeViewer.hintHandle, activeViewer.badgeHandle, activeViewer.statusHandle:
		return true
	default:
		return false
	}
}

func viewerLabelColor(handle uintptr) uintptr {
	switch handle {
	case activeViewer.brandHandle:
		return viewerAccentTextColor
	case activeViewer.titleHandle:
		return viewerPrimaryTextColor
	case activeViewer.badgeHandle:
		return viewerStrongTextColor
	case activeViewer.statusHandle:
		return viewerStrongTextColor
	default:
		return viewerMutedTextColor
	}
}

func enterViewerEditMode() {
	if activeViewer.editHandle == 0 {
		return
	}
	sendMessageWProc.Call(activeViewer.editHandle, emSetReadOnly, 0, 0)

	raw := strings.ReplaceAll(activeViewer.rawContent, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	raw = strings.ReplaceAll(raw, "\n", "\r\n")
	textPtr, _ := syscall.UTF16PtrFromString(raw)
	sendMessageWProc.Call(activeViewer.editHandle, wmSetText, 0, uintptr(unsafe.Pointer(textPtr)))
	applyViewerEditTheme()

	sendViewerText(activeViewer.badgeHandle, "EDIT MODE")
	sendViewerText(activeViewer.hintHandle, "Edit mode. Modify text, then Save to write or View to return to read-only.")
	sendViewerText(activeViewer.editButton, "View")
	showWindowProc.Call(activeViewer.saveButton, swShow)
	showWindowProc.Call(activeViewer.undoButton, swShow)

	activeViewer.editMode = true
	if activeViewer.hwnd != 0 {
		layoutViewerControls(activeViewer.hwnd)
	}
	updateViewerStatus()
}

func exitViewerEditMode() {
	if activeViewer.editHandle == 0 {
		return
	}
	sendMessageWProc.Call(activeViewer.editHandle, emSetReadOnly, 1, 0)

	data, err := os.ReadFile(activeViewer.filePath)
	if err == nil {
		activeViewer.rawContent = decodeFileContent(data, activeViewer.encoding)
		activeViewer.lineCount = viewerLineCount(activeViewer.rawContent)
		activeViewer.displayed = formatViewerText(activeViewer.rawContent)
		sendViewerText(activeViewer.metaHandle, viewerMetaText(activeViewer.filePath, activeViewer.lineCount))
	}

	textPtr, _ := syscall.UTF16PtrFromString(activeViewer.displayed)
	sendMessageWProc.Call(activeViewer.editHandle, wmSetText, 0, uintptr(unsafe.Pointer(textPtr)))
	applyViewerEditTheme()

	sendViewerText(activeViewer.badgeHandle, "READ ONLY")
	sendViewerText(activeViewer.hintHandle, viewerHintText())
	sendViewerText(activeViewer.editButton, "Edit")
	showWindowProc.Call(activeViewer.saveButton, swHide)
	showWindowProc.Call(activeViewer.undoButton, swHide)

	activeViewer.editMode = false
	if activeViewer.hwnd != 0 {
		layoutViewerControls(activeViewer.hwnd)
	}
	updateViewerStatus()
}

func getEditControlText() string {
	if activeViewer.editHandle == 0 {
		return ""
	}
	n, _, _ := sendMessageWProc.Call(activeViewer.editHandle, wmGettextlength, 0, 0)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	sendMessageWProc.Call(activeViewer.editHandle, wmGettext, n+1, uintptr(unsafe.Pointer(&buf[0])))
	return syscall.UTF16ToString(buf)
}

func saveViewerFile() error {
	content := getEditControlText()
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if activeViewer.useCRLF {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}

	switch activeViewer.encoding {
	case encodingUTF8BOM:
		data := append([]byte{0xEF, 0xBB, 0xBF}, []byte(content)...)
		return os.WriteFile(activeViewer.filePath, data, 0o644)
	case encodingUTF16LE:
		u16 := utf16.Encode([]rune(content))
		buf := make([]byte, 2+len(u16)*2)
		buf[0] = 0xFF
		buf[1] = 0xFE
		for i, u := range u16 {
			buf[2+i*2] = byte(u)
			buf[2+i*2+1] = byte(u >> 8)
		}
		return os.WriteFile(activeViewer.filePath, buf, 0o644)
	case encodingUTF16BE:
		u16 := utf16.Encode([]rune(content))
		buf := make([]byte, 2+len(u16)*2)
		buf[0] = 0xFE
		buf[1] = 0xFF
		for i, u := range u16 {
			buf[2+i*2] = byte(u >> 8)
			buf[2+i*2+1] = byte(u)
		}
		return os.WriteFile(activeViewer.filePath, buf, 0o644)
	case encodingANSI:
		return os.WriteFile(activeViewer.filePath, utf8ToCpAcp(content), 0o644)
	default:
		return os.WriteFile(activeViewer.filePath, []byte(content), 0o644)
	}
}

func detectFileEncoding(data []byte) fileEncoding {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return encodingUTF8BOM
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		return encodingUTF16LE
	}
	if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		return encodingUTF16BE
	}
	if utf8.Valid(data) {
		return encodingUTF8
	}
	return encodingANSI
}

func decodeFileContent(data []byte, enc fileEncoding) string {
	switch enc {
	case encodingUTF8BOM:
		return string(data[3:])
	case encodingUTF16LE:
		payload := data[2:]
		if len(payload)%2 != 0 {
			payload = payload[:len(payload)-1]
		}
		u16 := make([]uint16, len(payload)/2)
		for i := range u16 {
			u16[i] = uint16(payload[i*2]) | uint16(payload[i*2+1])<<8
		}
		return string(utf16.Decode(u16))
	case encodingUTF16BE:
		payload := data[2:]
		if len(payload)%2 != 0 {
			payload = payload[:len(payload)-1]
		}
		u16 := make([]uint16, len(payload)/2)
		for i := range u16 {
			u16[i] = uint16(payload[i*2])<<8 | uint16(payload[i*2+1])
		}
		return string(utf16.Decode(u16))
	case encodingANSI:
		return cpAcpToUTF8(data)
	default:
		return string(data)
	}
}

// cpAcpToUTF8 converts bytes in the system ANSI code page (CP_ACP) to a UTF-8 string.
// On Korean Windows this decodes CP949/EUC-KR correctly.
func cpAcpToUTF8(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	const cpACP = 0
	n, _, _ := multiByteToWideCharProc.Call(
		cpACP, 0,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		0, 0,
	)
	if n == 0 {
		return string(data)
	}
	buf := make([]uint16, n)
	multiByteToWideCharProc.Call(
		cpACP, 0,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&buf[0])),
		n,
	)
	return string(utf16.Decode(buf))
}

// utf8ToCpAcp converts a UTF-8 string to bytes in the system ANSI code page.
func utf8ToCpAcp(content string) []byte {
	if content == "" {
		return []byte{}
	}
	const cpACP = 0
	u16 := utf16.Encode([]rune(content))
	n, _, _ := wideCharToMultiByteProc.Call(
		cpACP, 0,
		uintptr(unsafe.Pointer(&u16[0])),
		uintptr(len(u16)),
		0, 0, 0, 0,
	)
	if n == 0 {
		return []byte(content)
	}
	buf := make([]byte, n)
	wideCharToMultiByteProc.Call(
		cpACP, 0,
		uintptr(unsafe.Pointer(&u16[0])),
		uintptr(len(u16)),
		uintptr(unsafe.Pointer(&buf[0])),
		n, 0, 0,
	)
	return buf
}

func writeViewerSelectionResult() error {
	if strings.TrimSpace(activeViewer.resultPath) == "" {
		return nil
	}

	selection, ok := currentViewerSelection()
	if !ok {
		return os.WriteFile(activeViewer.resultPath, []byte(""), 0o644)
	}
	data, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	return os.WriteFile(activeViewer.resultPath, data, 0o644)
}

func currentViewerSelection() (ViewerSelection, bool) {
	if activeViewer.editHandle == 0 || activeViewer.editMode {
		return ViewerSelection{}, false
	}
	var cr charRange
	sendMessageWProc.Call(activeViewer.editHandle, emExGetSel, 0, uintptr(unsafe.Pointer(&cr)))
	if cr.Max <= cr.Min {
		return ViewerSelection{}, false
	}
	start := lineNumberFromUTF16Offset(activeViewer.displayed, int(cr.Min))
	end := lineNumberFromUTF16Offset(activeViewer.displayed, int(cr.Max)-1)
	if end < start {
		end = start
	}
	return ViewerSelection{
		FilePath:  activeViewer.filePath,
		StartLine: start,
		EndLine:   end,
	}, true
}

func updateViewerStatus() {
	if activeViewer.statusHandle == 0 {
		return
	}
	if activeViewer.editMode {
		sendViewerText(activeViewer.statusHandle, "Edit mode — modify text, then Save to write changes or View to return to read-only.")
		return
	}
	selection, ok := currentViewerSelection()
	sendViewerText(activeViewer.statusHandle, viewerSelectionStatusText(activeViewer.lineCount, selection, ok))
}

func sendViewerText(handle uintptr, text string) {
	if handle == 0 {
		return
	}
	textPtr, _ := syscall.UTF16PtrFromString(text)
	sendMessageWProc.Call(handle, wmSetText, 0, uintptr(unsafe.Pointer(textPtr)))
}

func lineNumberFromUTF16Offset(content string, offset int) int {
	if offset < 0 {
		offset = 0
	}
	units := utf16.Encode([]rune(content))
	if offset > len(units) {
		offset = len(units)
	}
	line := 1
	for i := 0; i < offset; i++ {
		if units[i] == '\n' {
			line++
		}
	}
	return line
}

func viewerLineCount(content string) int {
	return len(viewerTextLines(content))
}

func viewerTextLines(content string) []string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func formatViewerText(content string) string {
	lines := viewerTextLines(content)
	width := len(fmt.Sprintf("%d", len(lines)))
	formatted := make([]string, 0, len(lines))
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		formatted = append(formatted, fmt.Sprintf("%*d | %s", width, i+1, line))
	}
	return strings.Join(formatted, "\r\n")
}

func viewerMetaText(path string, lineCount int) string {
	clean := compactViewerPath(filepath.Clean(path), 96)
	return fmt.Sprintf("%s  |  %d %s", clean, lineCount, pluralizeWord("line", lineCount))
}

func viewerHintText() string {
	return "Read-only code view. Select a range, then close the window to carry it into your next prompt or selection-based commands."
}

func viewerSelectionStatusText(lineCount int, selection ViewerSelection, ok bool) string {
	if !ok || !selection.HasSelection() {
		return fmt.Sprintf("No lines selected yet. %d %s loaded. Select a range and close the window to keep it for Kernforge.", lineCount, pluralizeWord("line", lineCount))
	}
	if selection.StartLine == selection.EndLine {
		return fmt.Sprintf("Selected line %d. Close the window to use this range in the next prompt.", selection.StartLine)
	}
	count := selection.EndLine - selection.StartLine + 1
	return fmt.Sprintf("Selected lines %d-%d (%d %s). Close the window to use this range in the next prompt.", selection.StartLine, selection.EndLine, count, pluralizeWord("line", count))
}

func compactViewerPath(path string, max int) string {
	if max <= 8 || len(path) <= max {
		return path
	}
	prefix := (max - 3) / 2
	suffix := max - 3 - prefix
	if prefix < 4 {
		prefix = 4
		suffix = max - 3 - prefix
	}
	if suffix < 4 {
		suffix = 4
		prefix = max - 3 - suffix
	}
	if prefix+suffix+3 >= len(path) {
		return path
	}
	return path[:prefix] + "..." + path[len(path)-suffix:]
}

func pluralizeWord(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

func int32ToUintptr(value int32) uintptr {
	return uintptr(uint32(value))
}

func rgb(r, g, b byte) uintptr {
	return uintptr(uint32(r) | uint32(g)<<8 | uint32(b)<<16)
}

func createViewerBrush(color uintptr) uintptr {
	handle, _, _ := createSolidBrushProc.Call(color)
	return handle
}

func destroyViewerResources() {
	for _, handle := range []uintptr{
		activeViewer.codeFontHandle,
		activeViewer.titleFontHandle,
		activeViewer.metaFontHandle,
		activeViewer.hintFontHandle,
		activeViewer.badgeFontHandle,
		activeViewer.statusFontHandle,
		activeViewer.buttonFontHandle,
		activeViewer.backgroundBrush,
		activeViewer.editorBrush,
	} {
		if handle != 0 {
			deleteObjectProc.Call(handle)
		}
	}
	activeViewer = viewerState{}
}

func loadViewerIcons() (uintptr, uintptr) {
	exePath, err := os.Executable()
	if err != nil {
		return 0, 0
	}
	iconFileName := "kernforge.ico"
	iconPath := filepath.Join(filepath.Dir(exePath), iconFileName)
	iconPtr, err := syscall.UTF16PtrFromString(iconPath)
	if err != nil {
		return 0, 0
	}

	big, _, _ := loadImageWProc.Call(
		0,
		uintptr(unsafe.Pointer(iconPtr)),
		imageIcon,
		32,
		32,
		lrLoadFromFile|lrDefaultSize,
	)
	small, _, _ := loadImageWProc.Call(
		0,
		uintptr(unsafe.Pointer(iconPtr)),
		imageIcon,
		16,
		16,
		lrLoadFromFile|lrDefaultSize,
	)
	return big, small
}

func viewerWindowPosition(width, height int32) (int32, int32) {
	screenW, _, _ := getSystemMetricsProc.Call(smCxScreen)
	screenH, _, _ := getSystemMetricsProc.Call(smCyScreen)
	margin := int32(16)

	x := (int32(screenW) - width) / 2
	y := (int32(screenH) - height) / 2
	if x < margin {
		x = margin
	}
	if y < margin {
		y = margin
	}
	return x, y
}
