package logging

import (
	"fmt"
	"io"
	"os"
	"time"
)

var (
	ShortLine = ""
	LongLine  = ""

	Output io.Writer = os.Stderr
)

func init() {
	for i := 0; i < 48; i++ {
		ShortLine += "-"
	}
	for i := 0; i < 64; i++ {
		LongLine += "-"
	}
}

func Printf(format string, a ...interface{}) (n int, err error) {
	return fmt.Fprintf(Output, NowString()+" "+format, a...)
}

func Println(a ...interface{}) (n int, err error) {
	a = append([]interface{}{NowString()}, a...)
	return fmt.Fprintln(Output, a...)
}

func Fatalf(format string, a ...interface{}) {
	Printf(format, a...)
	os.Exit(1)
}

func PrintShortLine() {
	fmt.Fprintln(Output, ShortLine)
}

func PrintLongLine() {
	fmt.Fprintln(Output, LongLine)
}

func Print(a ...interface{}) {
	fmt.Fprint(Output, a...)
}

func NowString() string {
	return time.Now().Format("20060102 15:04.05.000")
}
