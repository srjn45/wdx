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

	ErrorCodeNameTaken:             codeInfo(http.StatusConflict, clikit.ExitRejected, "the account has a node of that name already"),
	ErrorCodeRateLimited:           codeInfo(http.StatusTooManyRequests, clikit.ExitRejected, "too many tries from this address: wait and try again"),
	ErrorCodeNodeRevoked:           codeInfo(http.StatusForbidden, clikit.ExitRejected, "the node was removed from its account, or never was in one"),
	ErrorCodeAccountDisabled:       codeInfo(http.StatusForbidden, clikit.ExitRejected, "the node's account is disabled"),
	ErrorCodeHubVersionUnsupported: codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the hub serves no version of the API the node speaks"),
	ErrorCodeInvalidGrant:          codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the PKCE verifier is not the one the login started with"),
	ErrorCodeAuthorizationPending:  codeInfo(http.StatusConflict, clikit.ExitRejected, "the login is not approved yet"),
	ErrorCodeSlowDown:              codeInfo(http.StatusTooManyRequests, clikit.ExitRejected, "polled faster than the login's interval"),
	ErrorCodeAccessDenied:          codeInfo(http.StatusForbidden, clikit.ExitRejected, "the person denied the login"),
	ErrorCodeExpiredToken:          codeInfo(http.StatusGone, clikit.ExitRejected, "the login expired before it was approved and finished"),

	ErrorCodeRevisionGap:  codeInfo(http.StatusConflict, clikit.ExitRejected, "the revision is not the doc's next on the hub: a bug in the node"),
	ErrorCodeDocTooLarge:  codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the doc's content is more than a doc can hold"),
	ErrorCodeDocNameTaken: codeInfo(http.StatusConflict, clikit.ExitRejected, "another of the user's docs in the repository has that name on the hub"),
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
