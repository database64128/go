// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"internal/syscall/windows"
	"syscall"
	"unsafe"
)

// Stat returns the FileInfo structure describing file.
// If there is an error, it will be of type *PathError.
func (file *File) Stat() (FileInfo, error) {
	if file == nil {
		return nil, ErrInvalid
	}
	if file.isdir() {
		// I don't know any better way to do that for directory
		return Stat(file.dirinfo.path)
	}
	return statHandle(file.name, file.pfd.Sysfd)
}

// stat implements both Stat and Lstat of a file.
func stat(funcname, name string, createFileAttrs uint32) (FileInfo, error) {
	if len(name) == 0 {
		return nil, &PathError{Op: funcname, Path: name, Err: syscall.Errno(syscall.ERROR_PATH_NOT_FOUND)}
	}
	namep, err := syscall.UTF16PtrFromString(fixLongPath(name))
	if err != nil {
		return nil, &PathError{Op: funcname, Path: name, Err: err}
	}

	// Try GetFileAttributesEx first, because it is faster than CreateFile.
	// See https://golang.org/issues/19922#issuecomment-300031421 for details.
	var fa syscall.Win32FileAttributeData
	err = syscall.GetFileAttributesEx(namep, syscall.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&fa)))
	if err == nil && fa.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		// Not a symlink.
		fs := &fileStat{
			FileAttributes: fa.FileAttributes,
			CreationTime:   fa.CreationTime,
			LastAccessTime: fa.LastAccessTime,
			LastWriteTime:  fa.LastWriteTime,
			FileSizeHigh:   fa.FileSizeHigh,
			FileSizeLow:    fa.FileSizeLow,
		}
		if err := fs.saveInfoFromPath(name); err != nil {
			return nil, err
		}
		return fs, nil
	}
	// GetFileAttributesEx fails with ERROR_SHARING_VIOLATION error for
	// files, like c:\pagefile.sys. Use FindFirstFile for such files.
	if err == windows.ERROR_SHARING_VIOLATION {
		var fd syscall.Win32finddata
		sh, err := syscall.FindFirstFile(namep, &fd)
		if err != nil {
			return nil, &PathError{Op: "FindFirstFile", Path: name, Err: err}
		}
		syscall.FindClose(sh)
		fs := newFileStatFromWin32finddata(&fd)
		if err := fs.saveInfoFromPath(name); err != nil {
			return nil, err
		}
		return fs, nil
	}

	// Finally use CreateFile.
	h, err := syscall.CreateFile(namep,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil,
		syscall.OPEN_EXISTING,
		createFileAttrs, 0)
	if err != nil {
		return nil, &PathError{Op: "CreateFile", Path: name, Err: err}
	}
	defer syscall.CloseHandle(h)
	return statHandle(name, h)
}

func statHandle(name string, h syscall.Handle) (FileInfo, error) {
	ft, err := syscall.GetFileType(h)
	if err != nil {
		return nil, &PathError{Op: "GetFileType", Path: name, Err: err}
	}
	switch ft {
	case syscall.FILE_TYPE_PIPE, syscall.FILE_TYPE_CHAR:
		return &fileStat{name: basename(name), filetype: ft}, nil
	}
	fs, err := newFileStatFromGetFileInformationByHandle(name, h)
	if err != nil {
		return nil, err
	}
	fs.filetype = ft
	return fs, err
}

// statNolog implements Stat for Windows.
func statNolog(name string) (FileInfo, error) {
	return stat("Stat", name, syscall.FILE_FLAG_BACKUP_SEMANTICS)
}

// lstatNolog implements Lstat for Windows.
func lstatNolog(name string) (FileInfo, error) {
	attrs := uint32(syscall.FILE_FLAG_BACKUP_SEMANTICS)
	// Use FILE_FLAG_OPEN_REPARSE_POINT, otherwise CreateFile will follow symlink.
	// See https://docs.microsoft.com/en-us/windows/desktop/FileIO/symbolic-link-effects-on-file-systems-functions#createfile-and-createfiletransacted
	attrs |= syscall.FILE_FLAG_OPEN_REPARSE_POINT
	return stat("Lstat", name, attrs)
}
