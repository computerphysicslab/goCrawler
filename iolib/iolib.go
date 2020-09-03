// Package iolib provides I/O functions beyond goLang primitives
package iolib

import (
	"io"
	"io/ioutil"
	"log"
	"os"
)

/***************************************************************************************************************
****************************************************************************************************************
* I/O functions ************************************************************************************************
****************************************************************************************************************
****************************************************************************************************************/

// FileExists returns true if there is a file w/ that name
func FileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// CopyFileContents copies the contents of the file named src to the file named
// by dst. The file will be created if it does not already exist. If the
// destination file exists, all it's contents will be replaced by the contents
// of the source file.
func CopyFileContents(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer func() {
		cerr := out.Close()
		if err == nil {
			err = cerr
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return
	}
	err = out.Sync()
	return
}

// String2file saves the string content into a file
func String2file(text string, filename string) {
	aFile, err := os.Create(filename)
	if err != nil {
		log.Println(err)
	}
	aFile.Write([]byte(text))
}

// String2fileAppend appends the string content into a file
func String2fileAppend(text string, filename string) {
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text + "\n"); err != nil {
		log.Println(err)
	}
}

// File2string reads a file into a string
func File2string(filename string) string {
	file, err := os.Open(filename)
	if err != nil {
		log.Println(err)
	}
	defer file.Close()
	b, err2 := ioutil.ReadAll(file)
	if err2 != nil {
		log.Println(err)
	}

	return string(b[:])
}

// // Test shows package functionality
// func Test() {
// }
