package main

import "errors"

var ErrPromptCanceled = errors.New("prompt canceled")
var ErrEditCanceled = errors.New("edit canceled")
var ErrEditTargetMismatch = errors.New("edit target mismatch")
var ErrInvalidEditPayload = errors.New("invalid edit payload")
var ErrInvalidPatchFormat = errors.New("invalid patch format")
var ErrWriteDenied = errors.New("write denied")
