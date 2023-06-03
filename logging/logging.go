package logging

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

var (
	ShortLine = strings.Repeat("-", 62) + "\n"
	LongLine  = strings.Repeat("-", 100) + "\n"

	Output io.Writer = os.Stderr
)

func Printf(format string, a ...interface{}) (n int, err error) {
	return fmt.Fprintf(Output, NowString()+" "+format+"\n", a...)
}

func Println(a ...interface{}) (n int, err error) {
	a = append([]interface{}{NowString()}, a...)
	return fmt.Fprintln(Output, a...)
}

func Fatalf(format string, a ...interface{}) {
	Printf(format, a...)
	os.Exit(1)
}

func Print(a ...interface{}) {
	fmt.Fprint(Output, a...)
}

func NowString() string {
	return time.Now().Format("20060102 15:04.05.000")
}
