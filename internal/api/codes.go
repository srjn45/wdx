package api

import "net/http"

// Exit codes of the CLI.
const (
	ExitOK       = 0
	ExitRejected = 1 // a conflict, not found or cancelled
	ExitUsage    = 2
	ExitSystem   = 3
)

// CodeInfo is what an error code means and how it is reported.
type CodeInfo struct {
	Status      int // HTTP status
	Exit        int // CLI exit code
	Description string
}

// Codes describes every error code in the spec.
var Codes = map[ErrorCode]CodeInfo{
	ErrorCodeUsage:             {http.StatusBadRequest, ExitUsage, "the request, command line or a setting is invalid"},
	ErrorCodeInternal:          {http.StatusInternalServerError, ExitSystem, "an unexpected failure: database, filesystem or git"},
	ErrorCodeNotFound:          {http.StatusNotFound, ExitRejected, "no project has this name or id"},
	ErrorCodeNameTaken:         {http.StatusConflict, ExitRejected, "another project has this name"},
	ErrorCodeRepoExists:        {http.StatusConflict, ExitRejected, "the directory is already in a git repository"},
	ErrorCodeCheckoutMissing:   {http.StatusConflict, ExitRejected, "none of the project's checkouts is left on disk"},
	ErrorCodeNotGitRepo:        {http.StatusUnprocessableEntity, ExitRejected, "the path is not in a git repository"},
	ErrorCodeInvalidName:       {http.StatusUnprocessableEntity, ExitRejected, "the project name is not allowed"},
	ErrorCodeInvalidPath:       {http.StatusUnprocessableEntity, ExitRejected, "the path cannot be used"},
	ErrorCodeInvalidArgument:   {http.StatusUnprocessableEntity, ExitRejected, "an argument is empty or malformed"},
	ErrorCodePathExists:        {http.StatusConflict, ExitRejected, "the directory to clone into already exists"},
	ErrorCodeProjectMismatch:   {http.StatusConflict, ExitRejected, "the repository is a worktree of one project but has the origin of another"},
	ErrorCodeCloneFailed:       {http.StatusBadGateway, ExitRejected, "git clone failed"},
	ErrorCodeDaemonUnavailable: {http.StatusServiceUnavailable, ExitSystem, "the daemon is not running or not answering"},
	ErrorCodeAlreadyRunning:    {http.StatusConflict, ExitRejected, "a daemon already serves this home"},
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
