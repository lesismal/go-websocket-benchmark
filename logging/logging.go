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
	for i := 0; i < 32; i++ {
		ShortLine += "-"
	}
	ShortLine += "\n"
	for i := 0; i < 48; i++ {
		LongLine += "-"
	}
	LongLine += "\n"
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

func Print(a ...interface{}) {
	fmt.Fprint(Output, a...)
}

func NowString() string {
	return time.Now().Format("20060102 15:04.05.000")
}
