package hubapi

import (
	"net/http"

	"github.com/srjn45/wdx/internal/clikit"
)

// Codes describes every error code in the spec.
var Codes = map[ErrorCode]clikit.CodeInfo{
	ErrorCodeUsage:           codeInfo(http.StatusBadRequest, clikit.ExitUsage, "the request, command line or a setting is invalid"),
	ErrorCodeInternal:        codeInfo(http.StatusInternalServerError, clikit.ExitSystem, "an unexpected failure: database or filesystem"),
	ErrorCodeNotFound:        codeInfo(http.StatusNotFound, clikit.ExitRejected, "nothing has this name or id"),
	ErrorCodeInvalidArgument: codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "an argument is empty or malformed"),
	ErrorCodePathExists:      codeInfo(http.StatusConflict, clikit.ExitRejected, "something is already at the path"),
	ErrorCodeInvalidSetting:  codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the setting cannot take that value, or cannot be set"),
	ErrorCodeReloadFailed:    codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the hub cannot use the config file and keeps the settings it has"),
	ErrorCodeAlreadyRunning:  codeInfo(http.StatusConflict, clikit.ExitRejected, "a hub already serves this home"),
	ErrorCodeHubUnavailable:  codeInfo(http.StatusServiceUnavailable, clikit.ExitSystem, "the hub is not running or not answering"),
	ErrorCodeAuditBroken:     codeInfo(http.StatusConflict, clikit.ExitRejected, "a row of the audit chain does not match its hash: the chain was changed"),
	ErrorCodeCancelled:       codeInfo(http.StatusConflict, clikit.ExitRejected, "the question was answered no"),
}

func codeInfo(status, exit int, description string) clikit.CodeInfo {
	return clikit.CodeInfo{Status: status, Exit: exit, Description: description}
}

// NewProblem returns the problem of code, with the status and title the code
// has.
func NewProblem(code ErrorCode, detail string) Problem {
	info := Codes[code]
	return Problem{
		Type:   "urn:wdx:error:" + string(code),
		Title:  info.Description,
		Status: info.Status,
		Detail: detail,
		Code:   code,
	}
}
