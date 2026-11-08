package clikit

import "errors"

// What clikit's external tests reach of its internals.
var (
	RegisterCompletions = registerCompletions
	WrapRun             = wrapRun
	RuleLines           = ruleLines
)

// PanicStack is the stack of the panic err is, if it is one.
func PanicStack(err error) ([]byte, bool) {
	p, ok := errors.AsType[*panicError](err)
	if !ok {
		return nil, false
	}
	return p.stack, true
}

// ProblemOf is the problem a run reports for err.
func ProblemOf(env Env, err error) Problem { return problemOf(env, err) }
