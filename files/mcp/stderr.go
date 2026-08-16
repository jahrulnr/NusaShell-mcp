package main

import (
	"fmt"
	"os"
)

func stderr(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[nusashell-files] "+format+"\n", args...)
}
