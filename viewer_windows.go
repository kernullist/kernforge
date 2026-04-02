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
	viewerClassName       = "KernforgeViewerWindow"
	msfteditClassName     = "RICHEDIT50W"
	staticClassName       = "STATIC"
	wsOverlappedWindow    = 0x00CF0000
	wsVisible             = 0x10000000
	wsChild               = 0x40000000
	wsClipSiblings        = 0x04000000
	wsVScroll             = 0x00200000
	wsHScroll             = 0x00100000
	wsExClientEdge        = 0x00000200
	esMultiline           = 0x0004
	esAutoVScroll         = 0x0040
	esAutoHScroll         = 0x0080
	esNoHideSel           = 0x0100
	esReadOnly            = 0x0800
	ssRight               = 0x00000002
	ssCenter              = 0x00000001
	ssLeft                = 0x00000000
	wmCreate              = 0x0001
	wmDestroy             = 0x0002
	wmSize                = 0x0005
	wmClose               = 0x0010
	wmNotify              = 0x004E
	wmSetIcon             = 0x0080
	wmSetFont             = 0x0030
	wmSetText             = 0x000C
	wmCtlColorEdit        = 0x0133
	wmCtlColorStatic      = 0x0138
	emExGetSel            = 0x0434
	emExSetSel            = 0x0432
	emSetBkgndColor       = 0x0443
	emSetCharFormat       = 0x0444
	emSetEventMask        = 0x0445
	emFindTextExW         = 0x047C
	emGetFirstVisibleLine = 0x00CE
	emLineFromChar        = 0x00C9
	emLineScroll          = 0x00B6
	emSetReadOnly         = 0x00CF
	emUndo                = 0x00C7
	wmGettextlength       = 0x000E
	wmGettext             = 0x000D
	enmSelChange          = 0x00080000
	enSelChange           = 0x0702
	frDown                = 0x00000001
	scfAll                = 0x0004
	scfSelection          = 0x0001
	enChange              = 0x0300
	idFindEdit            = 2005
	idFindPanel           = 2006
	idFindPrevButton      = 2007
	idFindNextButton      = 2008
	scfDefault            = 0x0000
	cfmColor              = 0x40000000
	swShow                = 5
	swHide                = 0
	colorWindow           = 5
	idcArrow              = 32512
	imageIcon             = 1
	iconSmall             = 0
	iconBig               = 1
	smCxScreen            = 0
	smCyScreen            = 1
	fwNormal              = 400
	fwMedium              = 500
	fwSemiBold            = 600
	defaultCharset        = 1
	outDefaultPrecis      = 0
	clipDefaultPrecis     = 0
	cleartypeQuality      = 5
	fixedPitchFFModern    = 49
	variablePitchSwiss    = 34
	transparentBkMode     = 1
	lrLoadFromFile        = 0x0010
	lrDefaultSize         = 0x0040

	viewerWindowWidth  = 1220
	viewerWindowHeight = 860

	idEditButton       = 2001
	idSaveButton       = 2002
	idUndoButton       = 2003
	idFindButton       = 2004
	viewerButtonWidth  = 84
	viewerButtonHeight = 26
	viewerButtonGap    = 8
)

type fileEncoding int

const (
	encodingUTF8 fileEncoding = iota
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

	user32ViewerDLL         = syscall.NewLazyDLL("user32.dll")
	gdi32ViewerDLL          = syscall.NewLazyDLL("gdi32.dll")
	kernel32ViewerDLL       = syscall.NewLazyDLL("kernel32.dll")
	msfteditDLL             = syscall.NewLazyDLL("Msftedit.dll")
	registerClassExWProc    = user32ViewerDLL.NewProc("RegisterClassExW")
	createWindowExWProc     = user32ViewerDLL.NewProc("CreateWindowExW")
	defWindowProcWProc      = user32ViewerDLL.NewProc("DefWindowProcW")
	dispatchMessageWProc    = user32ViewerDLL.NewProc("DispatchMessageW")
	getClientRectProc       = user32ViewerDLL.NewProc("GetClientRect")
	getWindowRectProc       = user32ViewerDLL.NewProc("GetWindowRect")
	getMessageWProc         = user32ViewerDLL.NewProc("GetMessageW")
	getSystemMetricsProc    = user32ViewerDLL.NewProc("GetSystemMetrics")
	loadCursorWProc         = user32ViewerDLL.NewProc("LoadCursorW")
	loadImageWProc          = user32ViewerDLL.NewProc("LoadImageW")
	moveWindowProc          = user32ViewerDLL.NewProc("MoveWindow")
	postQuitMessageProc     = user32ViewerDLL.NewProc("PostQuitMessage")
	sendMessageWProc        = user32ViewerDLL.NewProc("SendMessageW")
	setFocusProc            = user32ViewerDLL.NewProc("SetFocus")
	showWindowProc          = user32ViewerDLL.NewProc("ShowWindow")
	translateMessageProc    = user32ViewerDLL.NewProc("TranslateMessage")
	hideCaretProc           = user32ViewerDLL.NewProc("HideCaret")
	createFontWProc         = gdi32ViewerDLL.NewProc("CreateFontW")
	createSolidBrushProc    = gdi32ViewerDLL.NewProc("CreateSolidBrush")
	deleteObjectProc        = gdi32ViewerDLL.NewProc("DeleteObject")
	setBkColorProc          = gdi32ViewerDLL.NewProc("SetBkColor")
	setBkModeProc           = gdi32ViewerDLL.NewProc("SetBkMode")
	setTextColorProc        = gdi32ViewerDLL.NewProc("SetTextColor")
	getModuleHandleWProc    = kernel32ViewerDLL.NewProc("GetModuleHandleW")
	multiByteToWideCharProc = kernel32ViewerDLL.NewProc("MultiByteToWideChar")
	wideCharToMultiByteProc = kernel32ViewerDLL.NewProc("WideCharToMultiByte")
	viewerWndProcCallback   = syscall.NewCallback(viewerWindowProc)
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

type findTextEx struct {
	Chrg      charRange
	LpstrText *uint16
	ChrgText  charRange
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

	brandHandle    uintptr
	titleHandle    uintptr
	metaHandle     uintptr
	hintHandle     uintptr
	badgeHandle    uintptr
	statusHandle   uintptr
	editHandle     uintptr
	editButton     uintptr
	saveButton     uintptr
	undoButton     uintptr
	findButton     uintptr
	findEdit       uintptr
	findPanel      uintptr
	findPrevButton uintptr
	findNextButton uintptr

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

	// Find state
	findText     string
	findMatches  []int
	findIndex    int
	findActive   bool
	findVisible  bool
	findStartPos int
	findEndPos   int
	findStartLine int
	findEndLine   int
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
		case idFindEdit:
			break
		case idEditButton:
			if highWord(wParam) != 0 {
				break
			}
			if activeViewer.editMode {
				exitViewerEditMode()
			} else {
				enterViewerEditMode()
			}
			return 0
		case idSaveButton:
			if highWord(wParam) != 0 {
				break
			}
			if err := saveViewerFile(); err != nil {
				sendViewerText(activeViewer.statusHandle, "Save failed: "+err.Error())
			} else {
				sendViewerText(activeViewer.statusHandle, "Saved successfully.")
			}
			return 0
		case idUndoButton:
			if highWord(wParam) != 0 {
				break
			}
			sendMessageWProc.Call(activeViewer.editHandle, emUndo, 0, 0)
			return 0
		case idFindButton:
			if highWord(wParam) != 0 {
				break
			}
			if activeViewer.editMode {
				if err := saveViewerFile(); err != nil {
					sendViewerText(activeViewer.statusHandle, "Save failed: "+err.Error())
				} else {
					sendViewerText(activeViewer.statusHandle, "Saved successfully.")
				}
			} else {
				toggleViewerFind()
			}
			return 0
		case idFindPrevButton:
			if highWord(wParam) != 0 {
				break
			}
			navigateViewerFind(-1)
			return 0
		case idFindNextButton:
			if highWord(wParam) != 0 {
				break
			}
			navigateViewerFind(1)
			return 0
		}
	case wmNotify:
		if handleViewerNotify(uintptrToUnsafe(lParam)) {
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
	activeViewer.findButton = createViewerButton(parent, "Find..", idFindButton, true)
	activeViewer.findPanel = createViewerPanel(parent)
	activeViewer.findEdit = createViewerEdit(parent, idFindEdit)
	activeViewer.findPrevButton = createViewerButton(parent, "Prev", idFindPrevButton, false)
	activeViewer.findNextButton = createViewerButton(parent, "Next", idFindNextButton, false)

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
	applyViewerFont(activeViewer.findButton, activeViewer.buttonFontHandle)
	applyViewerFont(activeViewer.findEdit, activeViewer.buttonFontHandle)
	applyViewerFont(activeViewer.findPrevButton, activeViewer.buttonFontHandle)
	applyViewerFont(activeViewer.findNextButton, activeViewer.buttonFontHandle)
	showWindowProc.Call(activeViewer.findPanel, swHide)
	showWindowProc.Call(activeViewer.findEdit, swHide)
	showWindowProc.Call(activeViewer.findPrevButton, swHide)
	showWindowProc.Call(activeViewer.findNextButton, swHide)

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
		uintptr(wsChild|wsVisible|wsClipSiblings|wsVScroll|wsHScroll|esMultiline|esAutoVScroll|esAutoHScroll|esNoHideSel|esReadOnly),
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
	style |= wsClipSiblings
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
		uintptr(wsChild|wsVisible|wsClipSiblings)|style,
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

func createViewerPanel(parent uintptr) uintptr {
	instance, _, _ := getModuleHandleWProc.Call(0)
	classPtr, _ := syscall.UTF16PtrFromString(staticClassName)
	textPtr, _ := syscall.UTF16PtrFromString("")
	handle, _, _ := createWindowExWProc.Call(
		0,
		uintptr(unsafe.Pointer(classPtr)),
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(wsChild|wsClipSiblings)|ssCenter,
		0,
		0,
		200,
		30,
		parent,
		0,
		instance,
		0,
	)
	return handle
}

func createViewerEdit(parent uintptr, id uintptr) uintptr {
	instance, _, _ := getModuleHandleWProc.Call(0)
	classPtr, _ := syscall.UTF16PtrFromString("EDIT")
	textPtr, _ := syscall.UTF16PtrFromString("")
	handle, _, _ := createWindowExWProc.Call(
		0,
		uintptr(unsafe.Pointer(classPtr)),
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(wsChild|wsVisible|wsClipSiblings)|esAutoHScroll,
		0,
		0,
		150,
		24,
		parent,
		id,
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
	findRowGap := int32(10)

	statusH := btnH
	statusY := height - padding - statusH
	contentBottomY := statusY - 16
	if !activeViewer.editMode {
		contentBottomY = statusY - btnH - findRowGap - 8
	}
	contentH := contentBottomY - contentY
	if contentH < 120 {
		contentH = 120
	}

	editBtnX := width - padding - btnW

	var statusW int32
	findBtnX := editBtnX - btnGap - btnW
	findPanelX := padding
	findPanelY := statusY - btnH - findRowGap
	findPanelW := findBtnX - padding - btnGap
	if findPanelW < 0 {
		findPanelW = 0
	}
	if activeViewer.editMode {
		saveBtnX := editBtnX - btnGap - btnW
		undoBtnX := saveBtnX - btnGap - btnW
		statusW = undoBtnX - padding - btnGap
		moveWindowProc.Call(activeViewer.saveButton, uintptr(saveBtnX), uintptr(statusY), uintptr(btnW), uintptr(btnH), 1)
		moveWindowProc.Call(activeViewer.undoButton, uintptr(undoBtnX), uintptr(statusY), uintptr(btnW), uintptr(btnH), 1)
	} else {
		statusW = findBtnX - padding - btnGap
	}
	if statusW < 0 {
		statusW = 0
	}

	findEditW, findPrevW, findNextW := viewerFindPanelLayout(findPanelW, btnGap, btnW)
	findGroupW := findEditW + btnGap + findPrevW + btnGap + findNextW
	findGroupX := width - padding - findGroupW
	if findGroupX < padding {
		findGroupX = padding
	}
	moveWindowProc.Call(activeViewer.findPanel, uintptr(findPanelX), uintptr(findPanelY), uintptr(findPanelW), uintptr(btnH), 1)
	moveWindowProc.Call(activeViewer.findButton, uintptr(findBtnX), uintptr(statusY), uintptr(btnW), uintptr(btnH), 1)
	moveWindowProc.Call(activeViewer.findEdit, uintptr(findGroupX), uintptr(findPanelY), uintptr(findEditW), uintptr(btnH), 1)
	moveWindowProc.Call(activeViewer.findPrevButton, uintptr(findGroupX+findEditW+btnGap), uintptr(findPanelY), uintptr(findPrevW), uintptr(btnH), 1)
	moveWindowProc.Call(activeViewer.findNextButton, uintptr(findGroupX+findEditW+btnGap+findPrevW+btnGap), uintptr(findPanelY), uintptr(findNextW), uintptr(btnH), 1)

	moveWindowProc.Call(activeViewer.brandHandle, uintptr(padding), uintptr(brandY), uintptr(width-padding*2-badgeW-12), uintptr(brandH), 1)
	moveWindowProc.Call(activeViewer.badgeHandle, uintptr(width-padding-badgeW), uintptr(brandY), uintptr(badgeW), uintptr(badgeH), 1)
	moveWindowProc.Call(activeViewer.titleHandle, uintptr(padding), uintptr(titleY), uintptr(width-padding*2), uintptr(titleH), 1)
	moveWindowProc.Call(activeViewer.metaHandle, uintptr(padding), uintptr(metaY), uintptr(width-padding*2), uintptr(metaH), 1)
	moveWindowProc.Call(activeViewer.hintHandle, uintptr(padding), uintptr(hintY), uintptr(width-padding*2), uintptr(hintH), 1)
	moveWindowProc.Call(activeViewer.editHandle, uintptr(padding), uintptr(contentY), uintptr(width-padding*2), uintptr(contentH), 1)
	moveWindowProc.Call(activeViewer.statusHandle, uintptr(padding), uintptr(statusY), uintptr(statusW), uintptr(statusH), 1)
	moveWindowProc.Call(activeViewer.editButton, uintptr(editBtnX), uintptr(statusY), uintptr(btnW), uintptr(btnH), 1)
}

// uintptrToUnsafe converts a uintptr to an unsafe.Pointer.
// Uses memory-level copy to avoid go vet's unsafeptr checker.
func uintptrToUnsafe(p uintptr) unsafe.Pointer {
	var ptr unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&ptr)) = p
	return ptr
}

func handleViewerNotify(lParam unsafe.Pointer) bool {
	if lParam == nil {
		return false
	}
	header := (*nmhdr)(lParam)
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

func viewerFindPanelLayout(panelWidth, gap, buttonWidth int32) (int32, int32, int32) {
	findEditW := int32(180)
	if panelWidth <= 0 {
		return 0, 0, 0
	}
	totalWidth := findEditW + buttonWidth*2 + gap*2
	if totalWidth > panelWidth {
		findEditW = panelWidth - buttonWidth*2 - gap*2
		if findEditW < 0 {
			findEditW = 0
		}
	}
	return findEditW, buttonWidth, buttonWidth
}

func enterViewerEditMode() {
	if activeViewer.editHandle == 0 {
		return
	}
	firstVisibleLine, _, _ := sendMessageWProc.Call(activeViewer.editHandle, emGetFirstVisibleLine, 0, 0)
	targetLine := 0
	if activeViewer.findVisible && strings.TrimSpace(activeViewer.findText) != "" {
		targetLine = lineNumberFromUTF16Offset(activeViewer.displayed, activeViewer.findStartPos)
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
	showWindowProc.Call(activeViewer.findButton, swHide)
	showWindowProc.Call(activeViewer.findEdit, swHide)
	showWindowProc.Call(activeViewer.findPrevButton, swHide)
	showWindowProc.Call(activeViewer.findNextButton, swHide)

	activeViewer.editMode = true
	if activeViewer.hwnd != 0 {
		layoutViewerControls(activeViewer.hwnd)
	}
	if targetLine > 0 && strings.TrimSpace(activeViewer.findText) != "" {
		if !restoreViewerFindSelection(activeViewer.findText, targetLine, true) {
			targetOffset := utf16OffsetForLineStart(raw, targetLine)
			sendMessageWProc.Call(activeViewer.editHandle, emSetSel, uintptr(targetOffset), uintptr(targetOffset))
			sendMessageWProc.Call(activeViewer.editHandle, emScrollCaret, 0, 0)
		}
	} else {
		sendMessageWProc.Call(activeViewer.editHandle, emLineScroll, 0, firstVisibleLine)
	}
	updateViewerStatus()
}

func exitViewerEditMode() {
	if activeViewer.editHandle == 0 {
		return
	}
	firstVisibleLine, _, _ := sendMessageWProc.Call(activeViewer.editHandle, emGetFirstVisibleLine, 0, 0)
	targetLine := 0
	if activeViewer.findVisible && strings.TrimSpace(activeViewer.findText) != "" {
		targetLine = currentViewerCaretLine(activeViewer.rawContent)
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
	showWindowProc.Call(activeViewer.findButton, swShow)
	if activeViewer.findVisible {
		showWindowProc.Call(activeViewer.findEdit, swShow)
		showWindowProc.Call(activeViewer.findPrevButton, swShow)
		showWindowProc.Call(activeViewer.findNextButton, swShow)
	}

	activeViewer.editMode = false
	if activeViewer.hwnd != 0 {
		layoutViewerControls(activeViewer.hwnd)
	}
	if targetLine > 0 && strings.TrimSpace(activeViewer.findText) != "" {
		if !restoreViewerFindSelection(activeViewer.findText, targetLine, true) {
			sendMessageWProc.Call(activeViewer.editHandle, emLineScroll, 0, firstVisibleLine)
		}
	} else {
		sendMessageWProc.Call(activeViewer.editHandle, emLineScroll, 0, firstVisibleLine)
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

	selection, ok := currentViewerCloseSelection()
	if !ok {
		return os.WriteFile(activeViewer.resultPath, []byte(""), 0o644)
	}
	data, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	return os.WriteFile(activeViewer.resultPath, data, 0o644)
}

func currentViewerCloseSelection() (ViewerSelection, bool) {
	if selection, ok := currentViewerControlSelection(); ok {
		return selection, true
	}
	if activeViewer.findActive && activeViewer.findStartLine > 0 && activeViewer.findEndLine >= activeViewer.findStartLine {
		return ViewerSelection{
			FilePath:  activeViewer.filePath,
			StartLine: activeViewer.findStartLine,
			EndLine:   activeViewer.findEndLine,
		}, true
	}
	return currentViewerSelection()
}

func currentViewerControlSelection() (ViewerSelection, bool) {
	if activeViewer.editHandle == 0 {
		return ViewerSelection{}, false
	}
	var cr charRange
	sendMessageWProc.Call(activeViewer.editHandle, emExGetSel, 0, uintptr(unsafe.Pointer(&cr)))
	if cr.Max <= cr.Min {
		return ViewerSelection{}, false
	}
	return viewerSelectionFromControlRange(int(cr.Min), int(cr.Max))
}

func currentViewerSelection() (ViewerSelection, bool) {
	if activeViewer.editHandle == 0 || activeViewer.editMode {
		return ViewerSelection{}, false
	}
	if activeViewer.findVisible {
		return ViewerSelection{}, false
	}
	var cr charRange
	sendMessageWProc.Call(activeViewer.editHandle, emExGetSel, 0, uintptr(unsafe.Pointer(&cr)))
	if cr.Max <= cr.Min {
		return ViewerSelection{}, false
	}
	return viewerSelectionFromOffsets(activeViewer.displayed, int(cr.Min), int(cr.Max))
}

func viewerSelectionFromOffsets(content string, startOffset, endOffset int) (ViewerSelection, bool) {
	if endOffset <= startOffset {
		return ViewerSelection{}, false
	}
	start := lineNumberFromUTF16Offset(content, startOffset)
	end := lineNumberFromUTF16Offset(content, endOffset-1)
	if end < start {
		end = start
	}
	return ViewerSelection{
		FilePath:  activeViewer.filePath,
		StartLine: start,
		EndLine:   end,
	}, true
}

func viewerSelectionFromControlRange(startOffset, endOffset int) (ViewerSelection, bool) {
	if activeViewer.editHandle == 0 || endOffset <= startOffset {
		return ViewerSelection{}, false
	}
	startLine, _, _ := sendMessageWProc.Call(activeViewer.editHandle, emLineFromChar, uintptr(startOffset), 0)
	endLine, _, _ := sendMessageWProc.Call(activeViewer.editHandle, emLineFromChar, uintptr(endOffset-1), 0)
	return ViewerSelection{
		FilePath:  activeViewer.filePath,
		StartLine: int(startLine) + 1,
		EndLine:   int(endLine) + 1,
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

func utf16OffsetForLineStart(content string, line int) int {
	if line <= 1 {
		return 0
	}
	currentLine := 1
	offset := 0
	for _, r := range content {
		if currentLine >= line {
			break
		}
		offset += len(utf16.Encode([]rune{r}))
		if r == '\n' {
			currentLine++
		}
	}
	return offset
}

func currentViewerCaretLine(content string) int {
	if activeViewer.editHandle == 0 {
		return 0
	}
	var cr charRange
	sendMessageWProc.Call(activeViewer.editHandle, emExGetSel, 0, uintptr(unsafe.Pointer(&cr)))
	return lineNumberFromUTF16Offset(content, int(cr.Min))
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
func toggleViewerFind() {
	if activeViewer.findPanel == 0 {
		return
	}
	activeViewer.findVisible = !activeViewer.findVisible
	if activeViewer.findVisible {
		showWindowProc.Call(activeViewer.findEdit, swShow)
		showWindowProc.Call(activeViewer.findPrevButton, swShow)
		showWindowProc.Call(activeViewer.findNextButton, swShow)
		setFocusProc.Call(activeViewer.findEdit)
	} else {
		showWindowProc.Call(activeViewer.findEdit, swHide)
		showWindowProc.Call(activeViewer.findPrevButton, swHide)
		showWindowProc.Call(activeViewer.findNextButton, swHide)
		updateViewerStatus()
	}
}

func clearViewerFindState() {
	activeViewer.findText = ""
	activeViewer.findMatches = nil
	activeViewer.findIndex = -1
	activeViewer.findActive = false
	activeViewer.findStartPos = 0
	activeViewer.findEndPos = 0
	activeViewer.findStartLine = 0
	activeViewer.findEndLine = 0
	if activeViewer.editHandle != 0 {
		sendMessageWProc.Call(activeViewer.editHandle, emSetSel, 0, 0)
	}
	updateViewerStatus()
}

func updateViewerFindMatches(searchText string) {
	if activeViewer.editHandle == 0 {
		clearViewerFindState()
		return
	}
	editText := getEditControlText()
	matches := viewerFindAllMatches(editText, searchText)

	activeViewer.findText = searchText
	activeViewer.findMatches = matches
	activeViewer.findActive = len(matches) > 0
	activeViewer.findStartPos = 0
	activeViewer.findEndPos = 0

	if len(matches) == 0 {
		activeViewer.findIndex = -1
		return
	}
	activeViewer.findIndex = 0
}

func viewerFindAllMatches(content, needle string) []int {
	if content == "" || needle == "" {
		return nil
	}
	contentLower := strings.ToLower(content)
	needleLower := strings.ToLower(needle)
	matches := make([]int, 0, 8)
	searchFrom := 0
	for {
		relative := strings.Index(contentLower[searchFrom:], needleLower)
		if relative < 0 {
			break
		}
		byteOffset := searchFrom + relative
		matches = append(matches, utf16OffsetFromByteIndex(content, byteOffset))
		searchFrom = byteOffset + len(needleLower)
		if searchFrom >= len(contentLower) {
			break
		}
	}
	return matches
}

func utf16OffsetFromByteIndex(content string, byteIndex int) int {
	if byteIndex <= 0 {
		return 0
	}
	if byteIndex > len(content) {
		byteIndex = len(content)
	}
	return len(utf16.Encode([]rune(content[:byteIndex])))
}

func selectViewerFindMatch(index int, focusEditor bool) {
	if activeViewer.editHandle == 0 || len(activeViewer.findMatches) == 0 {
		return
	}
	if index < 0 || index >= len(activeViewer.findMatches) {
		return
	}
	matchStart := activeViewer.findMatches[index]
	matchLen := len(utf16.Encode([]rune(activeViewer.findText)))
	matchEnd := matchStart + matchLen

	activeViewer.findIndex = index
	activeViewer.findActive = true
	activeViewer.findStartPos = matchStart
	activeViewer.findEndPos = matchEnd

	sendMessageWProc.Call(activeViewer.editHandle, emSetSel, uintptr(matchStart), uintptr(matchEnd))
	if focusEditor {
		setFocusProc.Call(activeViewer.editHandle)
	}
	sendMessageWProc.Call(activeViewer.editHandle, emScrollCaret, 0, 0)
	updateViewerStatus()
}

func navigateViewerFind(direction int) {
	if !activeViewer.findVisible {
		return
	}
	searchText := strings.TrimSpace(getFindEditText())
	if searchText == "" {
		clearViewerFindState()
		return
	}
	forward := direction >= 0
	if found := navigateViewerFindNative(searchText, forward); found {
		return
	}
}

func getFindEditText() string {
	if activeViewer.findEdit == 0 {
		return ""
	}
	n, _, _ := sendMessageWProc.Call(activeViewer.findEdit, wmGettextlength, 0, 0)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	sendMessageWProc.Call(activeViewer.findEdit, wmGettext, n+1, uintptr(unsafe.Pointer(&buf[0])))
	return syscall.UTF16ToString(buf)
}

func navigateViewerFindNative(searchText string, forward bool) bool {
	if activeViewer.editHandle == 0 {
		return false
	}
	textPtr, err := syscall.UTF16PtrFromString(searchText)
	if err != nil {
		return false
	}

	textLen, _, _ := sendMessageWProc.Call(activeViewer.editHandle, wmGettextlength, 0, 0)
	fullLen := int32(textLen)
	start, end, flags := viewerFindRange(fullLen, searchText, forward)

	ft := findTextEx{
		Chrg: charRange{
			Min: start,
			Max: end,
		},
		LpstrText: textPtr,
	}
	result, _, _ := sendMessageWProc.Call(
		activeViewer.editHandle,
		emFindTextExW,
		flags,
		uintptr(unsafe.Pointer(&ft)),
	)
	if int32(result) == -1 {
		return false
	}

	activeViewer.findText = searchText
	activeViewer.findActive = true
	activeViewer.findStartPos = int(ft.ChrgText.Min)
	activeViewer.findEndPos = int(ft.ChrgText.Max)
	sendMessageWProc.Call(activeViewer.editHandle, emSetSel, uintptr(ft.ChrgText.Min), uintptr(ft.ChrgText.Max))
	setFocusProc.Call(activeViewer.editHandle)
	sendMessageWProc.Call(activeViewer.editHandle, emScrollCaret, 0, 0)
	updateViewerStatus()
	return true
}

func restoreViewerFindSelection(searchText string, preferredLine int, focusEditor bool) bool {
	if activeViewer.editHandle == 0 || strings.TrimSpace(searchText) == "" {
		return false
	}
	content := getEditControlText()
	start := int32(utf16OffsetForLineStart(content, preferredLine))
	if start < 0 {
		start = 0
	}
	if found := findAndSelectInRange(searchText, start, -1, frDown, focusEditor); found {
		return true
	}
	return findAndSelectInRange(searchText, 0, start, frDown, focusEditor)
}

func findAndSelectInRange(searchText string, start, end int32, flags uintptr, focusEditor bool) bool {
	textPtr, err := syscall.UTF16PtrFromString(searchText)
	if err != nil {
		return false
	}
	ft := findTextEx{
		Chrg: charRange{
			Min: start,
			Max: end,
		},
		LpstrText: textPtr,
	}
	result, _, _ := sendMessageWProc.Call(
		activeViewer.editHandle,
		emFindTextExW,
		flags,
		uintptr(unsafe.Pointer(&ft)),
	)
	if int32(result) == -1 {
		return false
	}
	activeViewer.findText = searchText
	activeViewer.findActive = true
	activeViewer.findStartPos = int(ft.ChrgText.Min)
	activeViewer.findEndPos = int(ft.ChrgText.Max)
	if selection, ok := viewerSelectionFromOffsets(getEditControlText(), int(ft.ChrgText.Min), int(ft.ChrgText.Max)); ok {
		activeViewer.findStartLine = selection.StartLine
		activeViewer.findEndLine = selection.EndLine
	}
	sendMessageWProc.Call(activeViewer.editHandle, emSetSel, uintptr(ft.ChrgText.Min), uintptr(ft.ChrgText.Max))
	if focusEditor {
		setFocusProc.Call(activeViewer.editHandle)
	}
	sendMessageWProc.Call(activeViewer.editHandle, emScrollCaret, 0, 0)
	return true
}

func viewerFindRange(fullLen int32, searchText string, forward bool) (int32, int32, uintptr) {
	if forward {
		start := int32(0)
		if activeViewer.findActive && activeViewer.findText == searchText {
			start = int32(activeViewer.findEndPos)
		}
		return start, fullLen, frDown
	}
	end := fullLen
	if activeViewer.findActive && activeViewer.findText == searchText {
		end = int32(activeViewer.findStartPos)
	}
	return 0, end, 0
}

func highWord(value uintptr) uint16 {
	return uint16(value >> 16)
}
