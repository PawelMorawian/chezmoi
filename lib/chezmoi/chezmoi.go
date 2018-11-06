package chezmoi

import (
	"archive/tar"
	"bytes"
	"io/ioutil"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/absfs/afero"
	"github.com/pkg/errors"
)

var (
	dirNameRegexp         = regexp.MustCompile(`\A(?P<private>private_)?(?P<dot>dot_)?(?P<name>.*)\z`)
	dirNameSubexpIndexes  = makeSubexpIndexes(dirNameRegexp)
	fileNameRegexp        = regexp.MustCompile(`\A(?P<private>private_)?(?P<executable>executable_)?(?P<dot>dot_)?(?P<name>.*?)(?P<template>\.tmpl)?\z`)
	fileNameSubexpIndexes = makeSubexpIndexes(fileNameRegexp)
)

// A FileState represents the target state of a file.
type FileState struct {
	SourceName string
	Mode       os.FileMode
	Contents   []byte
}

// A DirState represents the target state of a directory.
type DirState struct {
	SourceName string
	Mode       os.FileMode
	Dirs       map[string]*DirState
	Files      map[string]*FileState
}

// A RootState represents the root target state.
type RootState struct {
	Dirs  map[string]*DirState
	Files map[string]*FileState
}

// newDirState returns a new directory state.
func newDirState(sourceName string, mode os.FileMode) *DirState {
	return &DirState{
		SourceName: sourceName,
		Mode:       mode,
		Dirs:       make(map[string]*DirState),
		Files:      make(map[string]*FileState),
	}
}

// archive writes ds to w.
func (ds *DirState) archive(w *tar.Writer, dirName string, headerTemplate *tar.Header) error {
	header := *headerTemplate
	header.Typeflag = tar.TypeDir
	header.Name = dirName
	header.Mode = int64(ds.Mode & os.ModePerm)
	if err := w.WriteHeader(&header); err != nil {
		return err
	}
	for _, fileName := range sortedFileNames(ds.Files) {
		if err := ds.Files[fileName].archive(w, filepath.Join(dirName, fileName), headerTemplate); err != nil {
			return err
		}
	}
	for _, subDirName := range sortedDirNames(ds.Dirs) {
		if err := ds.Dirs[subDirName].archive(w, filepath.Join(dirName, subDirName), headerTemplate); err != nil {
			return err
		}
	}
	return nil
}

// ensure ensures that targetDir in fs matches ds.
func (ds *DirState) ensure(fs afero.Fs, targetDir string) error {
	fi, err := fs.Stat(targetDir)
	switch {
	case err == nil && fi.Mode().IsDir():
		if fi.Mode()&os.ModePerm != ds.Mode {
			if err := fs.Chmod(targetDir, ds.Mode); err != nil {
				return err
			}
		}
	case err == nil:
		if err := fs.RemoveAll(targetDir); err != nil {
			return err
		}
		fallthrough
	case os.IsNotExist(err):
		if err := fs.Mkdir(targetDir, ds.Mode); err != nil {
			return err
		}
	default:
		return err
	}
	for _, fileName := range sortedFileNames(ds.Files) {
		if err := ds.Files[fileName].ensure(fs, filepath.Join(targetDir, fileName)); err != nil {
			return err
		}
	}
	for _, dirName := range sortedDirNames(ds.Dirs) {
		if err := ds.Dirs[dirName].ensure(fs, filepath.Join(targetDir, dirName)); err != nil {
			return err
		}
	}
	return nil
}

// archive writes fs to w.
func (fs *FileState) archive(w *tar.Writer, fileName string, headerTemplate *tar.Header) error {
	header := *headerTemplate
	header.Typeflag = tar.TypeReg
	header.Name = fileName
	header.Size = int64(len(fs.Contents))
	header.Mode = int64(fs.Mode)
	if err := w.WriteHeader(&header); err != nil {
		return nil
	}
	_, err := w.Write(fs.Contents)
	return err
}

// ensure ensures that state of targetPath in fs matches fileState.
func (fileState *FileState) ensure(fs afero.Fs, targetPath string) error {
	fi, err := fs.Stat(targetPath)
	switch {
	case err == nil && fi.Mode().IsRegular() && fi.Mode()&os.ModePerm == fileState.Mode:
		f, err := fs.Open(targetPath)
		if err != nil {
			return err
		}
		defer f.Close()
		contents, err := ioutil.ReadAll(f)
		if err != nil {
			return errors.Wrap(err, targetPath)
		}
		if reflect.DeepEqual(contents, fileState.Contents) {
			return nil
		}
	case err == nil:
		if err := fs.RemoveAll(targetPath); err != nil {
			return err
		}
	case os.IsNotExist(err):
	default:
		return err
	}
	// FIXME atomically replace
	return afero.WriteFile(fs, targetPath, fileState.Contents, fileState.Mode)
}

// NewRootState creates a new RootState.
func NewRootState() *RootState {
	return &RootState{
		Dirs:  make(map[string]*DirState),
		Files: make(map[string]*FileState),
	}
}

// Archive writes rs to w.
func (rs *RootState) Archive(w *tar.Writer) error {
	currentUser, err := user.Current()
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(currentUser.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(currentUser.Gid)
	if err != nil {
		return err
	}
	group, err := user.LookupGroupId(currentUser.Gid)
	if err != nil {
		return err
	}
	now := time.Now()
	headerTemplate := tar.Header{
		Uid:        uid,
		Gid:        gid,
		Uname:      currentUser.Username,
		Gname:      group.Name,
		ModTime:    now,
		AccessTime: now,
		ChangeTime: now,
	}
	for _, fileName := range sortedFileNames(rs.Files) {
		if err := rs.Files[fileName].archive(w, fileName, &headerTemplate); err != nil {
			return err
		}
	}
	for _, dirName := range sortedDirNames(rs.Dirs) {
		if err := rs.Dirs[dirName].archive(w, dirName, &headerTemplate); err != nil {
			return err
		}
	}
	return nil
}

// Ensure ensures that targetDir in fs matches ds.
func (rs *RootState) Ensure(fs afero.Fs, targetDir string) error {
	for _, fileName := range sortedFileNames(rs.Files) {
		if err := rs.Files[fileName].ensure(fs, filepath.Join(targetDir, fileName)); err != nil {
			return err
		}
	}
	for _, dirName := range sortedDirNames(rs.Dirs) {
		if err := rs.Dirs[dirName].ensure(fs, filepath.Join(targetDir, dirName)); err != nil {
			return err
		}
	}
	return nil
}

// FindSourceFile returns the source FileState for the given target file name,
// or nil if it cannot be found.
func (rs *RootState) FindSourceFile(fileName string) *FileState {
	components := splitPathList(fileName)
	dirs, files := rs.Dirs, rs.Files
	for i := 0; i < len(components)-1; i++ {
		dir, ok := dirs[components[i]]
		if !ok {
			return nil
		}
		dirs, files = dir.Dirs, dir.Files
	}
	return files[components[len(components)-1]]
}

// Populate walks fs from sourceDir creating a target directory state. Any
// templates found are executed with data.
func (rs *RootState) Populate(fs afero.Fs, sourceDir string, data interface{}) error {
	return afero.Walk(fs, sourceDir, func(path string, fi os.FileInfo, err error) error {
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}
		switch {
		case fi.Mode().IsRegular():
			dirNames, fileName, mode, isTemplate, err := parseFilePath(relPath)
			if err != nil {
				return errors.Wrap(err, path)
			}
			dirs, files := rs.Dirs, rs.Files
			for _, dirName := range dirNames {
				dirs, files = dirs[dirName].Dirs, dirs[dirName].Files
			}
			r, err := fs.Open(path)
			if err != nil {
				return err
			}
			defer r.Close()
			contents, err := ioutil.ReadAll(r)
			if err != nil {
				return errors.Wrap(err, path)
			}
			if isTemplate {
				tmpl, err := template.New(path).Parse(string(contents))
				if err != nil {
					return errors.Wrap(err, path)
				}
				output := &bytes.Buffer{}
				if err := tmpl.Execute(output, data); err != nil {
					return errors.Wrap(err, path)
				}
				contents = output.Bytes()
			}
			files[fileName] = &FileState{
				SourceName: relPath,
				Mode:       mode,
				Contents:   contents,
			}
		case fi.Mode().IsDir():
			components := splitPathList(relPath)
			dirNames, modes, err := parseDirNameComponents(components)
			if err != nil {
				return errors.Wrap(err, path)
			}
			dirs := rs.Dirs
			for i := 0; i < len(dirNames)-1; i++ {
				dirs = dirs[dirNames[i]].Dirs
			}
			dirName := dirNames[len(dirNames)-1]
			mode := modes[len(modes)-1]
			dirs[dirName] = newDirState(relPath, mode)
		default:
			return errors.Errorf("unsupported file type: %s", path)
		}
		return nil
	})
}

func makeSubexpIndexes(re *regexp.Regexp) map[string]int {
	result := make(map[string]int)
	for index, name := range re.SubexpNames() {
		result[name] = index
	}
	return result
}

func makeDirName(name string, mode os.FileMode) string {
	dirName := ""
	if mode&os.FileMode(077) == os.FileMode(0) {
		dirName = "private_"
	}
	if strings.HasPrefix(name, ".") {
		dirName += "dot_" + strings.TrimPrefix(name, ".")
	} else {
		dirName += name
	}
	return dirName
}

func makeFileName(name string, mode os.FileMode, isTemplate bool) string {
	fileName := ""
	if mode&os.FileMode(077) == os.FileMode(0) {
		fileName = "private_"
	}
	if mode&os.FileMode(0111) != os.FileMode(0) {
		fileName += "executable_"
	}
	if strings.HasPrefix(name, ".") {
		fileName += "dot_" + strings.TrimPrefix(name, ".")
	} else {
		fileName += name
	}
	if isTemplate {
		fileName += ".tmpl"
	}
	return fileName
}

// parseDirName parses a single directory name. It returns the target name,
// mode, and any error.
func parseDirName(dirName string) (string, os.FileMode, error) {
	m := dirNameRegexp.FindStringSubmatch(dirName)
	if m == nil {
		return "", os.FileMode(0), errors.Errorf("invalid directory name: %s", dirName)
	}
	name := m[dirNameSubexpIndexes["name"]]
	if m[dirNameSubexpIndexes["dot"]] != "" {
		name = "." + name
	}
	mode := os.FileMode(0777)
	if m[dirNameSubexpIndexes["private"]] != "" {
		mode &= 0700
	}
	return name, mode, nil
}

// parseFileName parses a single file name. It returns the target name, mode,
// whether the contents should be interpreted as a template, and any error.
func parseFileName(fileName string) (string, os.FileMode, bool, error) {
	m := fileNameRegexp.FindStringSubmatch(fileName)
	if m == nil {
		return "", os.FileMode(0), false, errors.Errorf("invalid file name: %s", fileName)
	}
	name := m[fileNameSubexpIndexes["name"]]
	if m[fileNameSubexpIndexes["dot"]] != "" {
		name = "." + name
	}
	mode := os.FileMode(0666)
	if m[fileNameSubexpIndexes["executable"]] != "" {
		mode |= 0111
	}
	if m[fileNameSubexpIndexes["private"]] != "" {
		mode &= 0700
	}
	isTemplate := m[fileNameSubexpIndexes["template"]] != ""
	return name, mode, isTemplate, nil
}

// parseDirNameComponents parses multiple directory name components. It returns
// the target directory names, target modes, and any error.
func parseDirNameComponents(components []string) ([]string, []os.FileMode, error) {
	dirNames := []string{}
	modes := []os.FileMode{}
	for _, component := range components {
		dirName, mode, err := parseDirName(component)
		if err != nil {
			return nil, nil, err
		}
		dirNames = append(dirNames, dirName)
		modes = append(modes, mode)
	}
	return dirNames, modes, nil
}

// parseFilePath parses a single file path. It returns the target directory
// names, the target filename, the target mode, whether the contents should be
// interpreted as a template, and any error.
func parseFilePath(path string) ([]string, string, os.FileMode, bool, error) {
	if path == "" {
		return nil, "", os.FileMode(0), false, errors.New("empty path")
	}
	components := splitPathList(path)
	dirNames, _, err := parseDirNameComponents(components[0 : len(components)-1])
	if err != nil {
		return nil, "", os.FileMode(0), false, err
	}
	fileName, mode, isTemplate, err := parseFileName(components[len(components)-1])
	if err != nil {
		return nil, "", os.FileMode(0), false, err
	}
	return dirNames, fileName, mode, isTemplate, nil
}

// sortedDirNames returns a sorted slice of all directory names in ds.
func sortedDirNames(dirs map[string]*DirState) []string {
	dirNames := []string{}
	for dirName := range dirs {
		dirNames = append(dirNames, dirName)
	}
	sort.Strings(dirNames)
	return dirNames
}

// sortedFileNames returns a sorted slice of all file names in ds.
func sortedFileNames(files map[string]*FileState) []string {
	fileNames := []string{}
	for fileName := range files {
		fileNames = append(fileNames, fileName)
	}
	sort.Strings(fileNames)
	return fileNames
}

func splitPathList(path string) []string {
	components := strings.Split(path, string(filepath.Separator))
	if components[0] == "" {
		return components[1:len(components)]
	}
	return components
}