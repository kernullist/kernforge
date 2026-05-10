package main

import "errors"

var ErrPromptCanceled = errors.New("prompt canceled")
var ErrRequestCanceled = errors.New("request canceled by user")
var ErrEditCanceled = errors.New("edit canceled")
var ErrEditTargetMismatch = errors.New("edit target mismatch")
var ErrInvalidEditPayload = errors.New("invalid edit payload")
var ErrInvalidPatchFormat = errors.New("invalid patch format")
var ErrInvalidToolArgumentsJSON = errors.New("invalid tool arguments json")
var ErrUserChangeConflict = errors.New("user change conflict")
var ErrWriteDenied = errors.New("write denied")
