package aca

import (
	"fmt"
	"io"
	"regexp"
)

// transcriptSessionHeading matches a session's heading in TranscriptFile and gives the
// session's id and the ACA's. Sessions rendered before the heading named the ACA have
// only the id.
var transcriptSessionHeading = regexp.MustCompile(`^# Session (\S+)(?: · (\S+))?$`)

// writeSessionHeading starts a session in TranscriptFile, naming the ACA that wrote
// it, so a transcript that holds sessions of more than one says which wrote what.
func writeSessionHeading(w io.Writer, session, acaID string) {
	fmt.Fprintf(w, "# Session %s · %s\n\n", session, acaID)
}
