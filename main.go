// Command owned-document-research answers a research question from owned
// document excerpts while exposing evidence conflicts, missing information,
// and source ownership.
//
// The command line is built task by task; docs/case-brief.md states the
// specification this repository implements.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "owned-document-research: the command line is not wired yet")
	os.Exit(2)
}
