package api

import (
	"net/http"

	"github.com/srjn45/wdx/internal/clikit"
)

// Codes describes every error code in the spec.
var Codes = map[ErrorCode]clikit.CodeInfo{
	ErrorCodeUsage:                 codeInfo(http.StatusBadRequest, clikit.ExitUsage, "the request, command line or a setting is invalid"),
	ErrorCodeInternal:              codeInfo(http.StatusInternalServerError, clikit.ExitSystem, "an unexpected failure: database, filesystem or git"),
	ErrorCodeNotFound:              codeInfo(http.StatusNotFound, clikit.ExitRejected, "nothing has this name or id"),
	ErrorCodeNameTaken:             codeInfo(http.StatusConflict, clikit.ExitRejected, "something else has this name"),
	ErrorCodeRepoExists:            codeInfo(http.StatusConflict, clikit.ExitRejected, "the directory is already in a git repository"),
	ErrorCodeCheckoutMissing:       codeInfo(http.StatusConflict, clikit.ExitRejected, "none of the project's checkouts is left on disk"),
	ErrorCodeNotGitRepo:            codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the path is not in a git repository"),
	ErrorCodeInvalidName:           codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the name is not allowed"),
	ErrorCodeInvalidPath:           codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the path cannot be used"),
	ErrorCodeInvalidArgument:       codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "an argument is empty or malformed"),
	ErrorCodePathExists:            codeInfo(http.StatusConflict, clikit.ExitRejected, "something is already at the path, as the directory to clone into"),
	ErrorCodeProjectMismatch:       codeInfo(http.StatusConflict, clikit.ExitRejected, "the repository is a worktree of one project but has the origin of another"),
	ErrorCodeCloneFailed:           codeInfo(http.StatusBadGateway, clikit.ExitRejected, "git clone failed"),
	ErrorCodeBranchExists:          codeInfo(http.StatusConflict, clikit.ExitRejected, "a new worktree's branch already exists"),
	ErrorCodeWorktreeFailed:        codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "git could not add the worktree"),
	ErrorCodeLandNotConfigured:     codeInfo(http.StatusConflict, clikit.ExitRejected, "the project has not said how it lands"),
	ErrorCodeAgentWorking:          codeInfo(http.StatusConflict, clikit.ExitRejected, "an agent is working in the worktree"),
	ErrorCodeNotCommitted:          codeInfo(http.StatusConflict, clikit.ExitRejected, "the worktree has files not committed"),
	ErrorCodeNothingToLand:         codeInfo(http.StatusConflict, clikit.ExitRejected, "the worktree has nothing ahead of its base"),
	ErrorCodeNoLocalBase:           codeInfo(http.StatusConflict, clikit.ExitRejected, "the worktree has no local branch to land on"),
	ErrorCodeDetachedHead:          codeInfo(http.StatusConflict, clikit.ExitRejected, "the worktree is on no branch, so a PR has none to push"),
	ErrorCodePushFailed:            codeInfo(http.StatusConflict, clikit.ExitRejected, "git refused to push the branch"),
	ErrorCodePrFailed:              codeInfo(http.StatusConflict, clikit.ExitRejected, "gh did not open the pull request"),
	ErrorCodeLandingRunning:        codeInfo(http.StatusConflict, clikit.ExitRejected, "the worktree is already landing"),
	ErrorCodeWorktreeBusy:          codeInfo(http.StatusConflict, clikit.ExitRejected, "a rebase, a merge or the like is in progress in the worktree"),
	ErrorCodeRebaseFailed:          codeInfo(http.StatusConflict, clikit.ExitRejected, "the rebase stopped and was undone"),
	ErrorCodeCheckFailed:           codeInfo(http.StatusConflict, clikit.ExitRejected, "the project's check failed"),
	ErrorCodeBaseMoved:             codeInfo(http.StatusConflict, clikit.ExitRejected, "the base moved during the check"),
	ErrorCodeFastForwardFailed:     codeInfo(http.StatusConflict, clikit.ExitRejected, "git would not move the base forward"),
	ErrorCodeWorktreeIsClone:       codeInfo(http.StatusConflict, clikit.ExitRejected, "the project's clone cannot be removed"),
	ErrorCodeWorktreeNotLanded:     codeInfo(http.StatusConflict, clikit.ExitRejected, "the worktree has commits or changes that have not landed"),
	ErrorCodeWorktreeNotRemoved:    codeInfo(http.StatusConflict, clikit.ExitRejected, "git would not remove the worktree"),
	ErrorCodeProjectClosed:         codeInfo(http.StatusConflict, clikit.ExitRejected, "the project is closed"),
	ErrorCodeProjectInUse:          codeInfo(http.StatusConflict, clikit.ExitRejected, "terminals or agents are running in the project"),
	ErrorCodeTerminalRunning:       codeInfo(http.StatusConflict, clikit.ExitRejected, "the terminal is already starting or running"),
	ErrorCodeStartFailed:           codeInfo(http.StatusInternalServerError, clikit.ExitSystem, "the holder, the shell or the AI coding agent did not start"),
	ErrorCodeTerminalNotRunning:    codeInfo(http.StatusConflict, clikit.ExitRejected, "the terminal's shell is not running"),
	ErrorCodeSendFailed:            codeInfo(http.StatusInternalServerError, clikit.ExitSystem, "the message did not reach the terminal's holder"),
	ErrorCodeAgentRunning:          codeInfo(http.StatusConflict, clikit.ExitRejected, "the agent is already starting or running"),
	ErrorCodeAgentRemoved:          codeInfo(http.StatusConflict, clikit.ExitRejected, "the agent was removed and stays only for its children"),
	ErrorCodeAgentNotRunning:       codeInfo(http.StatusConflict, clikit.ExitRejected, "the agent's AI coding agent is not running"),
	ErrorCodeAgentBusy:             codeInfo(http.StatusConflict, clikit.ExitRejected, "the agent is in the middle of something, which this would lose"),
	ErrorCodeAttachUnsupported:     codeInfo(http.StatusConflict, clikit.ExitRejected, "the terminal was started by a wdx too old to attach to"),
	ErrorCodeConnectionLost:        codeInfo(http.StatusServiceUnavailable, clikit.ExitSystem, "the connection to the terminal ended before its shell did"),
	ErrorCodeDaemonUnavailable:     codeInfo(http.StatusServiceUnavailable, clikit.ExitSystem, "the daemon is not running or not answering"),
	ErrorCodeAlreadyRunning:        codeInfo(http.StatusConflict, clikit.ExitRejected, "a daemon already serves this home"),
	ErrorCodeRuntimeMissing:        codeInfo(http.StatusServiceUnavailable, clikit.ExitSystem, "neither Docker nor Podman is installed"),
	ErrorCodePortInUse:             codeInfo(http.StatusConflict, clikit.ExitRejected, "a port the observability stack needs is taken"),
	ErrorCodeRuntimeFailed:         codeInfo(http.StatusBadGateway, clikit.ExitSystem, "Docker or Podman failed"),
	ErrorCodeDownloadFailed:        codeInfo(http.StatusBadGateway, clikit.ExitSystem, "the release could not be found or downloaded"),
	ErrorCodeChecksumMismatch:      codeInfo(http.StatusBadGateway, clikit.ExitSystem, "the downloaded release does not match its checksum"),
	ErrorCodeOlderVersion:          codeInfo(http.StatusConflict, clikit.ExitRejected, "the release is older than the installed version"),
	ErrorCodeUpdateRunning:         codeInfo(http.StatusConflict, clikit.ExitRejected, "another update or rollback is running"),
	ErrorCodeUpdateFailed:          codeInfo(http.StatusInternalServerError, clikit.ExitSystem, "the update or rollback failed and was undone"),
	ErrorCodeNoPreviousVersion:     codeInfo(http.StatusConflict, clikit.ExitRejected, "no previous version is kept to roll back to"),
	ErrorCodeRestoreRequired:       codeInfo(http.StatusConflict, clikit.ExitRejected, "rolling back needs the database backup, losing the changes since the update"),
	ErrorCodeSettingsInvalid:       codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the release does not accept a value the config file sets"),
	ErrorCodeInvalidSetting:        codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the setting cannot take that value, or cannot be set"),
	ErrorCodeReloadFailed:          codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the daemon cannot use the config file and keeps the settings it has"),
	ErrorCodeCancelled:             codeInfo(http.StatusConflict, clikit.ExitRejected, "the question was answered no"),
	ErrorCodeUnsupportedSystem:     codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "wdx cannot install it on this system: the instructions say how to by hand"),
	ErrorCodeInstallFailed:         codeInfo(http.StatusBadGateway, clikit.ExitSystem, "the package manager failed"),
	ErrorCodeDependencyMissing:     codeInfo(http.StatusServiceUnavailable, clikit.ExitSystem, "a program the request needs is not installed: wdx deps list tells which"),
	ErrorCodePrivilegesRequired:    codeInfo(http.StatusForbidden, clikit.ExitRejected, "installing needs root, and sudo cannot ask for the password"),
	ErrorCodeEditorRunning:         codeInfo(http.StatusConflict, clikit.ExitRejected, "an editor has the file, or a file in the directory, open"),
	ErrorCodeDocChanged:            codeInfo(http.StatusConflict, clikit.ExitRejected, "the doc has a newer revision than the base given"),
	ErrorCodeTideRunning:           codeInfo(http.StatusConflict, clikit.ExitRejected, "a TIDE is already running for this home"),
	ErrorCodeInvalidPlan:           codeInfo(http.StatusUnprocessableEntity, clikit.ExitRejected, "the plan's YAML has problems: details.problems lists them"),
	ErrorCodePlanChanged:           codeInfo(http.StatusConflict, clikit.ExitRejected, "the plan has a newer revision than the base given"),
	ErrorCodePlanInUse:             codeInfo(http.StatusConflict, clikit.ExitRejected, "a plan of plans lists the plan"),
	ErrorCodePlanStarted:           codeInfo(http.StatusConflict, clikit.ExitRejected, "the plan has run: only a draft can be changed or run"),
	ErrorCodeTaskState:             codeInfo(http.StatusConflict, clikit.ExitRejected, "the plan's task is not in a state the request needs"),
	ErrorCodePlanNotRunning:        codeInfo(http.StatusConflict, clikit.ExitRejected, "the plan is not running or paused"),
	ErrorCodePlanNotFinished:       codeInfo(http.StatusConflict, clikit.ExitRejected, "the plan is not done or cancelled, or a plan it waits for is not done"),
	ErrorCodePlanNotLanded:         codeInfo(http.StatusConflict, clikit.ExitRejected, "the work of a plan it waits for is not in the default branch"),
	ErrorCodeWorktreeInPlan:        codeInfo(http.StatusConflict, clikit.ExitRejected, "the worktree is a slot of a plan going on"),
	ErrorCodeBaseNotPulled:         codeInfo(http.StatusConflict, clikit.ExitRejected, "the PR below is merged on GitHub, but not in the local default branch"),
	ErrorCodeWrongBranch:           codeInfo(http.StatusConflict, clikit.ExitRejected, "the worktree is not on the branch the request needs"),
	ErrorCodeAcaUnsupported:        codeInfo(http.StatusConflict, clikit.ExitRejected, "the agent's AI coding agent cannot do this yet"),
	ErrorCodeAcaUnavailable:        codeInfo(http.StatusConflict, clikit.ExitRejected, "the AI coding agent is not installed or is disabled, or none is installed and enabled"),
	ErrorCodeAlreadyLoggedIn:       codeInfo(http.StatusConflict, clikit.ExitRejected, "this node is logged in to a hub already"),
	ErrorCodeNotLoggedIn:           codeInfo(http.StatusConflict, clikit.ExitRejected, "this node is not logged in to a hub, or the hub cut it off"),
	ErrorCodeLoginDenied:           codeInfo(http.StatusForbidden, clikit.ExitRejected, "the login was denied on the hub, or the account is disabled"),
	ErrorCodeLoginExpired:          codeInfo(http.StatusGone, clikit.ExitRejected, "the login was not approved in time"),
	ErrorCodeLoginCancelled:        codeInfo(http.StatusConflict, clikit.ExitRejected, "the login was cancelled, or another replaced it"),
	ErrorCodeHubUnreachable:        codeInfo(http.StatusBadGateway, clikit.ExitSystem, "the hub cannot be reached, or did not answer as a hub does"),
	ErrorCodeHubVersionUnsupported: codeInfo(http.StatusConflict, clikit.ExitRejected, "the hub serves no version of the API this wdx speaks"),
	ErrorCodeNodeNotFound:          codeInfo(http.StatusNotFound, clikit.ExitRejected, "the account has no node of this name or id"),
	ErrorCodeNodeIsThis:            codeInfo(http.StatusConflict, clikit.ExitRejected, "the node is this one: leaving is wdx hub logout"),
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
