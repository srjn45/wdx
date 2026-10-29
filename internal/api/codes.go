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
	ErrorCodeUsage:              {http.StatusBadRequest, ExitUsage, "the request, command line or a setting is invalid"},
	ErrorCodeInternal:           {http.StatusInternalServerError, ExitSystem, "an unexpected failure: database, filesystem or git"},
	ErrorCodeNotFound:           {http.StatusNotFound, ExitRejected, "nothing has this name or id"},
	ErrorCodeNameTaken:          {http.StatusConflict, ExitRejected, "something else has this name"},
	ErrorCodeRepoExists:         {http.StatusConflict, ExitRejected, "the directory is already in a git repository"},
	ErrorCodeCheckoutMissing:    {http.StatusConflict, ExitRejected, "none of the project's checkouts is left on disk"},
	ErrorCodeNotGitRepo:         {http.StatusUnprocessableEntity, ExitRejected, "the path is not in a git repository"},
	ErrorCodeInvalidName:        {http.StatusUnprocessableEntity, ExitRejected, "the name is not allowed"},
	ErrorCodeInvalidPath:        {http.StatusUnprocessableEntity, ExitRejected, "the path cannot be used"},
	ErrorCodeInvalidArgument:    {http.StatusUnprocessableEntity, ExitRejected, "an argument is empty or malformed"},
	ErrorCodePathExists:         {http.StatusConflict, ExitRejected, "the directory to clone into already exists"},
	ErrorCodeProjectMismatch:    {http.StatusConflict, ExitRejected, "the repository is a worktree of one project but has the origin of another"},
	ErrorCodeCloneFailed:        {http.StatusBadGateway, ExitRejected, "git clone failed"},
	ErrorCodeProjectClosed:      {http.StatusConflict, ExitRejected, "the project is closed"},
	ErrorCodeProjectInUse:       {http.StatusConflict, ExitRejected, "terminals are running in the project"},
	ErrorCodeTerminalRunning:    {http.StatusConflict, ExitRejected, "the terminal is already starting or running"},
	ErrorCodeStartFailed:        {http.StatusInternalServerError, ExitSystem, "the holder or the shell did not start"},
	ErrorCodeTerminalNotRunning: {http.StatusConflict, ExitRejected, "the terminal's shell is not running"},
	ErrorCodeAttachUnsupported:  {http.StatusConflict, ExitRejected, "the terminal was started by a wdx too old to attach to"},
	ErrorCodeConnectionLost:     {http.StatusServiceUnavailable, ExitSystem, "the connection to the terminal ended before its shell did"},
	ErrorCodeDaemonUnavailable:  {http.StatusServiceUnavailable, ExitSystem, "the daemon is not running or not answering"},
	ErrorCodeAlreadyRunning:     {http.StatusConflict, ExitRejected, "a daemon already serves this home"},
	ErrorCodeRuntimeMissing:     {http.StatusServiceUnavailable, ExitSystem, "neither Docker nor Podman is installed"},
	ErrorCodePortInUse:          {http.StatusConflict, ExitRejected, "a port the observability stack needs is taken"},
	ErrorCodeRuntimeFailed:      {http.StatusBadGateway, ExitSystem, "Docker or Podman failed"},
	ErrorCodeDownloadFailed:     {http.StatusBadGateway, ExitSystem, "the release could not be found or downloaded"},
	ErrorCodeChecksumMismatch:   {http.StatusBadGateway, ExitSystem, "the downloaded release does not match its checksum"},
	ErrorCodeOlderVersion:       {http.StatusConflict, ExitRejected, "the release is older than the installed version"},
	ErrorCodeUpdateRunning:      {http.StatusConflict, ExitRejected, "another update or rollback is running"},
	ErrorCodeUpdateFailed:       {http.StatusInternalServerError, ExitSystem, "the update or rollback failed and was undone"},
	ErrorCodeNoPreviousVersion:  {http.StatusConflict, ExitRejected, "no previous version is kept to roll back to"},
	ErrorCodeRestoreRequired:    {http.StatusConflict, ExitRejected, "rolling back needs the database backup, losing the changes since the update"},
	ErrorCodeSettingsInvalid:    {http.StatusUnprocessableEntity, ExitRejected, "the release does not accept a value the config file sets"},
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
