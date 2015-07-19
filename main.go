package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("deb2aci: package manifest")
		return
	}
	pkg, manifest := os.Args[1], os.Args[2]

	log.Printf("deb2aci: will convert package %v", pkg)
	image, err := filepath.Abs(fmt.Sprintf("./%v.aci", pkg))
	if err != nil {
		log.Fatalf("err: %v", err)
	}

	manifest, err = filepath.Abs(os.Args[2])
	if err != nil {
		log.Fatalf(err.Error())
	}
	if _, err := os.Stat(manifest); err != nil {
		log.Fatalf(err.Error())
	}

	if err := convert(pkg, image, manifest); err != nil {
		log.Fatalf("deb2aci: ERROR: %v", err)
	}
	log.Printf("deb2aci: here you go: %v", image)
}

func convert(pkg, image, manifest string) error {
	if pkg == "" || image == "" {
		return errorf("image name and package name can not be empty")
	}
	dir, err := ioutil.TempDir("", "deb2aci")
	if err != nil {
		return err
	}

	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("deb2aci: failed to remove %v, err: %v", dir, err)
		}
	}()

	fs := make(map[string]string)

	if err := download(pkg, dir, fs); err != nil {
		return err
	}
	return createACI(dir, fs, image, manifest)
}

func createACI(dir string, fs map[string]string, image, manifest string) error {
	idir, err := ioutil.TempDir(dir, "image")
	if err != nil {
		return errorf(err.Error())
	}
	rootfs := filepath.Join(idir, "rootfs")
	os.MkdirAll(rootfs, 0755)
	for _, path := range fs {
		err := run(exec.Command("cp", "-a", path+"/.", rootfs))
		if err != nil {
			return err
		}
	}
	if err := run(exec.Command("install", "-T", manifest, filepath.Join(idir, "manifest"))); err != nil {
		return err
	}
	if err := run(exec.Command("actool", "build", "-overwrite", idir, image)); err != nil {
		return err
	}
	return nil
}

func download(pkg, dir string, done map[string]string) error {
	log.Printf("downloading %v to %v", pkg, dir)

	if done[pkg] != "" {
		log.Printf("%v already downloaded, returning", pkg)
		return nil
	}

	tdir, err := ioutil.TempDir(dir, "pkg")
	if err != nil {
		return err
	}
	os.Chdir(tdir)

	err = run(exec.Command("apt-get", "download", pkg))
	if err != nil {
		return err
	}

	matches, err := filepath.Glob(filepath.Join(tdir, "*.deb"))
	if err != nil || len(matches) != 1 {
		return errorf("unexpected: %v %v", err, matches)
	}
	deb := matches[0]
	// now unpack the archive to the folder
	err = run(exec.Command(
		"dpkg-deb", "-x", deb, filepath.Join(tdir, "out")))
	if err != nil {
		return err
	}
	done[pkg] = filepath.Join(tdir, "out")

	// now list all dependencies
	out, err := exec.Command("dpkg-deb", "-f", deb, "Depends").CombinedOutput()
	if err != nil {
		return errorf("%v: %v", out, err.Error())
	}
	deps := parseDeps(string(out))
	if len(deps) != 0 {
		log.Printf("%v depends on %#v, downloading deps", pkg, deps)
		for _, d := range deps {
			if err := download(d, dir, done); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseDeps(line string) []string {
	line = strings.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}
	parts := strings.Split(line, ",")
	if len(parts) == 0 {
		return nil
	}
	deps := make([]string, len(parts))
	for i, p := range parts {
		o := strings.Split(strings.TrimSpace(p), " ")
		deps[i] = o[0]
	}
	return deps
}

func errorf(format string, args ...interface{}) error {
	msg := fmt.Sprintf(format, args...)
	pc, filePath, lineNo, ok := runtime.Caller(1)
	if !ok {
		return &Err{
			Message: msg,
			File:    "unknown_file",
			Path:    "unknown_path",
			Func:    "unknown_func",
			Line:    0,
		}
	}
	return &Err{
		Message: msg,
		File:    filepath.Base(filePath),
		Path:    filePath,
		Func:    runtime.FuncForPC(pc).Name(),
		Line:    lineNo,
	}
}

type Err struct {
	Message string
	File    string
	Path    string
	Func    string
	Line    int
}

func (e *Err) Error() string {
	return fmt.Sprintf("[%v:%v] %v", e.File, e.Line, e.Message)
}

func run(cmd *exec.Cmd) error {
	log.Printf("run: %v %v", cmd.Path, cmd.Args)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errorf(err.Error())
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return errorf(err.Error())
	}
	go io.Copy(os.Stdout, stdout)
	go io.Copy(os.Stderr, stderr)
	return cmd.Run()
}
