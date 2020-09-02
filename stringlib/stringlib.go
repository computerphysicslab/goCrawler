// Package stringlib provides string functions beyond goLang primitives
package stringlib

import (
	"fmt"
	"regexp"
	"strconv"
)

/***************************************************************************************************************
****************************************************************************************************************
* String functions *********************************************************************************************
****************************************************************************************************************
****************************************************************************************************************/

// RmNewLines removes any newline found on the input string
func RmNewLines(t string) string {
	var re = regexp.MustCompile(`(\n+)`)
	t = re.ReplaceAllString(t, "")

	return t
}

// IsNumeric tells whether input is a number or not
func IsNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// Test shows package functionality
func Test() {
	fmt.Printf("RmNewLines(1\n2\n): -%s-\n", RmNewLines("1\n2\n"))
	fmt.Printf("IsNumeric(hello world): %+v\n", IsNumeric("hello world"))
	fmt.Printf("IsNumeric(33): %+v\n", IsNumeric("33"))
}
