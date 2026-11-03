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
	ErrorCodePathExists:         {http.StatusConflict, ExitRejected, "something is already at the path, as the directory to clone into"},
	ErrorCodeProjectMismatch:    {http.StatusConflict, ExitRejected, "the repository is a worktree of one project but has the origin of another"},
	ErrorCodeCloneFailed:        {http.StatusBadGateway, ExitRejected, "git clone failed"},
	ErrorCodeBranchExists:       {http.StatusConflict, ExitRejected, "a new worktree's branch already exists"},
	ErrorCodeWorktreeFailed:     {http.StatusUnprocessableEntity, ExitRejected, "git could not add the worktree"},
	ErrorCodeLandNotConfigured:  {http.StatusConflict, ExitRejected, "the project has not said how it lands"},
	ErrorCodeAgentWorking:       {http.StatusConflict, ExitRejected, "an agent is working in the worktree"},
	ErrorCodeNotCommitted:       {http.StatusConflict, ExitRejected, "the worktree has files not committed"},
	ErrorCodeNothingToLand:      {http.StatusConflict, ExitRejected, "the worktree has nothing ahead of its base"},
	ErrorCodeNoLocalBase:        {http.StatusConflict, ExitRejected, "the worktree has no local branch to land on"},
	ErrorCodeDetachedHead:       {http.StatusConflict, ExitRejected, "the worktree is on no branch, so a PR has none to push"},
	ErrorCodePushFailed:         {http.StatusConflict, ExitRejected, "git refused to push the branch"},
	ErrorCodePrFailed:           {http.StatusConflict, ExitRejected, "gh did not open the pull request"},
	ErrorCodeLandingRunning:     {http.StatusConflict, ExitRejected, "the worktree is already landing"},
	ErrorCodeWorktreeBusy:       {http.StatusConflict, ExitRejected, "a rebase, a merge or the like is in progress in the worktree"},
	ErrorCodeRebaseFailed:       {http.StatusConflict, ExitRejected, "the rebase stopped and was undone"},
	ErrorCodeCheckFailed:        {http.StatusConflict, ExitRejected, "the project's check failed"},
	ErrorCodeBaseMoved:          {http.StatusConflict, ExitRejected, "the base moved during the check"},
	ErrorCodeFastForwardFailed:  {http.StatusConflict, ExitRejected, "git would not move the base forward"},
	ErrorCodeWorktreeIsClone:    {http.StatusConflict, ExitRejected, "the project's clone cannot be removed"},
	ErrorCodeWorktreeNotLanded:  {http.StatusConflict, ExitRejected, "the worktree has commits or changes that have not landed"},
	ErrorCodeWorktreeNotRemoved: {http.StatusConflict, ExitRejected, "git would not remove the worktree"},
	ErrorCodeProjectClosed:      {http.StatusConflict, ExitRejected, "the project is closed"},
	ErrorCodeProjectInUse:       {http.StatusConflict, ExitRejected, "terminals or agents are running in the project"},
	ErrorCodeTerminalRunning:    {http.StatusConflict, ExitRejected, "the terminal is already starting or running"},
	ErrorCodeStartFailed:        {http.StatusInternalServerError, ExitSystem, "the holder, the shell or the AI coding agent did not start"},
	ErrorCodeTerminalNotRunning: {http.StatusConflict, ExitRejected, "the terminal's shell is not running"},
	ErrorCodeSendFailed:         {http.StatusInternalServerError, ExitSystem, "the message did not reach the terminal's holder"},
	ErrorCodeAgentRunning:       {http.StatusConflict, ExitRejected, "the agent is already starting or running"},
	ErrorCodeAgentRemoved:       {http.StatusConflict, ExitRejected, "the agent was removed and stays only for its children"},
	ErrorCodeAgentNotRunning:    {http.StatusConflict, ExitRejected, "the agent's AI coding agent is not running"},
	ErrorCodeAgentBusy:          {http.StatusConflict, ExitRejected, "the agent is in the middle of something, which this would lose"},
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
	ErrorCodeInvalidSetting:     {http.StatusUnprocessableEntity, ExitRejected, "the setting cannot take that value, or cannot be set"},
	ErrorCodeReloadFailed:       {http.StatusUnprocessableEntity, ExitRejected, "the daemon cannot use the config file and keeps the settings it has"},
	ErrorCodeCancelled:          {http.StatusConflict, ExitRejected, "the question was answered no"},
	ErrorCodeUnsupportedSystem:  {http.StatusUnprocessableEntity, ExitRejected, "wdx cannot install it on this system: the instructions say how to by hand"},
	ErrorCodeInstallFailed:      {http.StatusBadGateway, ExitSystem, "the package manager failed"},
	ErrorCodeDependencyMissing:  {http.StatusServiceUnavailable, ExitSystem, "a program the request needs is not installed: wdx deps list tells which"},
	ErrorCodePrivilegesRequired: {http.StatusForbidden, ExitRejected, "installing needs root, and sudo cannot ask for the password"},
	ErrorCodeEditorRunning:      {http.StatusConflict, ExitRejected, "an editor has the file, or a file in the directory, open"},
	ErrorCodeDocChanged:         {http.StatusConflict, ExitRejected, "the doc has a newer revision than the base given"},
	ErrorCodeTideRunning:        {http.StatusConflict, ExitRejected, "a tide is already running for this home"},
	ErrorCodeInvalidPlan:        {http.StatusUnprocessableEntity, ExitRejected, "the plan's YAML has problems: details.problems lists them"},
	ErrorCodePlanChanged:        {http.StatusConflict, ExitRejected, "the plan has a newer revision than the base given"},
	ErrorCodePlanInUse:          {http.StatusConflict, ExitRejected, "a plan of plans lists the plan"},
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
