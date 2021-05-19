// Copyright 2015 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package pmemutil

/*
#cgo CFLAGS: -g -Wall
#cgo LDFLAGS: -lpmemlog -lpmem
#include <sys/stat.h>
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <string.h>
#include "libpmem.h"
#include <libpmemlog.h>

// size of the pmemlog pool -- 64MB
#define	PMEM_LEN 4096
#define BUF_LEN 4096

int byteToString(PMEMlogpool *plp, const unsigned char *buf, size_t len) {
	return pmemlog_append(plp, buf, len);
}

static int
printit(const void *buf, size_t len, void *arg)
{
	memcpy(arg, buf, len);
	return 0;
}

void logprint(PMEMlogpool *plp, unsigned char *out) {
	pmemlog_walk(plp, 0, printit, out);
}

int IsPmemTrue(char *path) {
	char *pmemaddr;
	size_t mapped_len;
	int is_pmem;

	if ((pmemaddr = pmem_map_file(path, PMEM_LEN,
				PMEM_FILE_CREATE|PMEM_FILE_EXCL,
				0666, &mapped_len, &is_pmem)) == NULL) {
		perror("Error - pmem_map_file");
		exit(1);
	}

	pmem_unmap(pmemaddr, mapped_len);
	return is_pmem;
}

PMEMlogpool *pmemlogCreateOrOpen(char *path, size_t poolSize, unsigned int mode) {
	PMEMlogpool *plp;
	plp= pmemlog_create(path, poolSize, mode);
	if (plp == NULL) {
		perror(path);
		plp = pmemlog_open(path);
	}
	if (plp == NULL) {
		perror(path);
	}
	return plp;
}

PMEMlogpool *pmemlogOpen(const char *path) {
	PMEMlogpool *plp;
	plp = pmemlog_open(path);
	if (plp == NULL) {
		perror(path);
		exit(1);
	}
	return plp;
}

void copy(const char *source, const char *destination) {
	int srcfd;
	char *pmemaddr;
        size_t mapped_len;
        int is_pmem;
	struct stat stbuf;

	if ((srcfd = open(source, O_RDONLY)) < 0) {
		perror(source);
		exit(1);
	}

	if (fstat(srcfd, &stbuf) < 0) {
		perror("fstat");
		exit(1);
	}

        if ((pmemaddr = pmem_map_file(destination, stbuf.st_size,
                                PMEM_FILE_CREATE|PMEM_FILE_EXCL,
                                0666, &mapped_len, &is_pmem)) == NULL) {
                perror("Error creating backup file on pmem");
                exit(1);
        }

	char buf[BUF_LEN];
	int cc;
	
	while ((cc = read(srcfd, buf, BUF_LEN)) > 0) {
		pmem_memcpy_nodrain(pmemaddr, buf, cc);
		pmemaddr += cc;
	}

	if (cc < 0) {
		perror("Error reading source file while copying the source file from pmem");
		exit(1);
	}

	pmem_drain();

	close(srcfd);
	pmem_unmap(pmemaddr, mapped_len);
}
*/
import "C"

import (
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"unsafe"

	"go.etcd.io/etcd/pkg/fileutil"
)

type Pmemlogpool *C.PMEMlogpool

const letterBytes = "abcdefghijklmnopqrstuvwxyz"

// RandStringBytesRmndr generates random string that is required for random filename
func RandStringBytesRmndr(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Int63()%int64(len(letterBytes))]
	}
	return string(b)
}

// IsPmemTrue checks if a particular directory path is in pmem or not
func IsPmemTrue(dirpath string) (bool, error) {
	path := filepath.Join(filepath.Clean(dirpath), RandStringBytesRmndr(5))

	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	isPmem := int(C.IsPmemTrue(cpath))
	err := os.Remove(path)
	if isPmem == 0 {
		return false, err
	}
	return true, err
}

// InitiatePmemLogPool initiates a log pool
func InitiatePmemLogPool(path string, poolSize int64) (err error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	plp := C.pmemlogCreateOrOpen(cpath, C.size_t(poolSize), C.uint(fileutil.PrivateFileMode))
	if plp == nil {
		err = errors.New("Failed to open pmem file")
	}
	defer func() {
		if plp != nil {
			Close(plp)
		}
	}()

	return err
}

// Seek gives the total bytes written in a particular file
func Seek(plp Pmemlogpool) int64 {
	defer Close(plp)
	return int64(C.pmemlog_tell(plp))
}

// ZeroToEndForPmem zeros a pmem file starting from SEEK_CUR to its SEEK_END. May temporarily
// shorten the length of the file.
func ZeroToEndForPmem(path string, f *os.File) error {
	off, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	Resize(path, off)

	_, err = f.Seek(off, io.SeekStart)
	return err
}

// Copy copies from source file to destination file in pmem
func Copy(source, destination string) {
	csource := C.CString(source)
        defer C.free(unsafe.Pointer(csource))

	cdestination := C.CString(destination)
        defer C.free(unsafe.Pointer(cdestination))

	C.copy(csource, cdestination)
}

// Resize resizes the pmem file. There was no better way found to resize.
func Resize(filePath string, off int64) (err error) {
	pr := OpenForRead(filePath)
	data := make([]byte, off)
	r, err := pr.Read(data)
	if err != nil {
		err = errors.New("Could not read data during resizing pmem file")
		return err
	}

	err = os.Remove(filePath)
	if err != nil {
		err = errors.New("Could not delete the file with bigger size while resizing pmem file")
		return err
	}

	if off < 2097152 {
		off = 2097152
	}
	err = InitiatePmemLogPool(filePath, off)
	if err != nil {
		return err
	}
	pw := OpenForWrite(filePath)
	w, err := pw.Write(data)
	if err != nil {
		err = errors.New("Could not write data during resizing pmem file")
		return err
	}

	if r != w {
		err = errors.New("Could not write same bytes what were read.")
		return err
	}

	return nil
}

// Close closes the logpool
func Close(plp Pmemlogpool) {
	C.pmemlog_close(plp)
}

// Pmemwriter structure just stores the buffer that would be written to pmem
type Pmemwriter struct {
	path string
}

// OpenForWrite returns the Pmem writer
func OpenForWrite(path string) (pw *Pmemwriter) {
	pw = &Pmemwriter{
		path: path,
	}
	return pw
}

// GetLogPool fetches the the log pool. Make sure you close this just after using the log pool.
func (pw *Pmemwriter) GetLogPool() (plp Pmemlogpool, err error) {
	cpath := C.CString(pw.path)
	defer C.free(unsafe.Pointer(cpath))

	plp = C.pmemlogOpen(cpath)
	if plp == nil {
		err = errors.New("Failed to open pmem file")
	}
	return plp, err
}

// Write writes len(b) bytes to the pmem buffer
func (pw *Pmemwriter) Write(b []byte) (n int, err error) {
	cpath := C.CString(pw.path)
	defer C.free(unsafe.Pointer(cpath))

	plp := C.pmemlogOpen(cpath)
	if plp == nil {
		err = errors.New("Failed to open pmem file for write")
	}
	defer Close(plp)

	ptr := C.malloc(C.size_t(len(b)))
	defer C.free(unsafe.Pointer(ptr))

	copy((*[1 << 24]byte)(ptr)[0:len(b)], b)
	cdata := C.CBytes(b)
	defer C.free(unsafe.Pointer(cdata))

	if plp != nil {
		if int(C.byteToString(plp, (*C.uchar)(cdata), C.size_t(len(string(b))))) < 0 {
			err = errors.New("Log could not be appended in pmem")
		}
	}
	return len(b), err
}

// Pmemreader implements buffering for io.Reader object
type Pmemreader struct {
	path string
	i    int64 // current reading index
}

// OpenForRead opens a pmemlog from a path and returns the Pmem reader
func OpenForRead(path string) (pr *Pmemreader) {
	pr = &Pmemreader{
		path: path,
		i:    0,
	}
	return pr
}

// GetLogPool fetches the the log pool. Make sure you close this just after using the log pool.
func (pr *Pmemreader) GetLogPool() (plp Pmemlogpool, err error) {
	cpath := C.CString(pr.path)
	defer C.free(unsafe.Pointer(cpath))

	plp = C.pmemlogOpen(cpath)
	if plp == nil {
		err = errors.New("Failed to open pmem file")
	}
	return plp, err
}

// Reader reads data into b
func (pr *Pmemreader) Read(b []byte) (n int, err error) {
	cpath := C.CString(pr.path)
	defer C.free(unsafe.Pointer(cpath))

	plp := C.pmemlogOpen(cpath)
	if plp == nil {
		err = errors.New("Failed to open pmem file for read")
	}
	defer Close(plp)

	length := C.pmemlog_tell(plp)

	ptr := C.malloc(C.size_t(length))
	defer C.free(unsafe.Pointer(ptr))

	C.logprint(plp, (*C.uchar)(ptr))
	s := C.GoBytes(ptr, C.int(length))
	if pr.i >= int64(len(s)) {
		return 0, io.EOF
	}
	n = copy(b, s[pr.i:])
	pr.i += int64(n)
	return
}

// Close is a placeholder
func (pr *Pmemreader) Close() (err error) {
	return nil
}
