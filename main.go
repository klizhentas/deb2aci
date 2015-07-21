package main

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/klizhentas/deb2aci/Godeps/_workspace/src/github.com/appc/spec/schema"
	"github.com/klizhentas/deb2aci/Godeps/_workspace/src/github.com/appc/spec/schema/types"
)

type pkgs []string

func (p *pkgs) String() string {
	return fmt.Sprintf("%v", *p)
}

func (p *pkgs) Set(value string) error {
	*p = append(*p, value)
	return nil
}

func main() {
	var pkgs pkgs
	flag.Var(&pkgs, "pkg", "list of packages to download")

	var image string
	flag.StringVar(&image, "image", "", "image name")

	var manifestPath string
	flag.StringVar(&manifestPath, "manifest", "", "manifest")

	if len(os.Args) < 3 {
		log.Fatalf("deb2aci: package package package manifest")
		return
	}
	flag.Parse()
	if len(pkgs) == 0 {
		log.Fatalf("supply at least one package")
	}
	if len(image) == 0 {
		log.Fatalf("provide an image name")
	}

	log.Printf(
		"deb2aci: will convert packages %v and archive to %v", pkgs, image)
	image, err := filepath.Abs(image)
	if err != nil {
		log.Fatalf("err: %v", err)
	}
	manifest, err := readManifest(manifestPath)
	if err != nil {
		log.Fatalf(err.Error())
	}
	if err := convert(pkgs, image, manifest); err != nil {
		log.Fatalf("deb2aci: ERROR: %v", err)
	}
	log.Printf("deb2aci: here you go: %v", image)
}

func readManifest(path string) (*schema.ImageManifest, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, errorf(err.Error())
	}
	i := schema.ImageManifest{}
	if err := i.UnmarshalJSON(b); err != nil {
		return nil, errorf(err.Error())
	}
	return &i, nil
}

func convert(pkgs []string, image string, manifest *schema.ImageManifest) error {
	dir, err := ioutil.TempDir("", "deb2aci")
	if err != nil {
		return err
	}

	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("deb2aci: failed to remove %v, err: %v", dir, err)
		}
	}()

	fs := make(map[string]*deb)
	for _, pkg := range pkgs {
		if err := download(pkg, dir, fs); err != nil {
			return err
		}
	}
	return createACI(dir, fs, image, manifest)
}

func createACI(dir string, fs map[string]*deb, image string, m *schema.ImageManifest) error {
	idir, err := ioutil.TempDir(dir, "image")
	if err != nil {
		return errorf(err.Error())
	}
	rootfs := filepath.Join(idir, "rootfs")
	os.MkdirAll(rootfs, 0755)

	for _, d := range fs {
		err := run(exec.Command("cp", "-a", d.Path+"/.", rootfs))
		if err != nil {
			return err
		}
		i, err := types.SanitizeACIdentifier(
			fmt.Sprintf("debian.org/deb/%v", d.Name))
		if err != nil {
			return errorf(err.Error())
		}
		a, err := types.NewACIdentifier(i)
		if err != nil {
			return errorf(err.Error())
		}
		m.Annotations.Set(
			*a, fmt.Sprintf("%v/%v", d.Arch, d.Version))
	}
	bytes, err := m.MarshalJSON()
	if err != nil {
		return errorf(err.Error())
	}
	if err := ioutil.WriteFile(filepath.Join(idir, "manifest"), bytes, 0644); err != nil {
		return errorf(err.Error())
	}
	if err := run(exec.Command("actool", "build", "-overwrite", idir, image)); err != nil {
		return err
	}
	return nil
}

func download(pkg, dir string, done map[string]*deb) error {
	log.Printf("downloading %v to %v", pkg, dir)

	if done[pkg] != nil {
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
	debName := matches[0]
	// now unpack the archive to the folder
	err = run(exec.Command(
		"dpkg-deb", "-x", debName, filepath.Join(tdir, "out")))
	if err != nil {
		return err
	}

	arch, err := output("dpkg-deb", "-f", debName, "Architecture")
	if err != nil {
		return err
	}

	ver, err := output("dpkg-deb", "-f", debName, "Version")
	if err != nil {
		return err
	}

	done[pkg] = &deb{
		Name:    pkg,
		Path:    filepath.Join(tdir, "out"),
		Arch:    arch,
		Version: ver,
	}

	// now list all dependencies
	out, err := output("dpkg-deb", "-f", debName, "Depends")
	if err != nil {
		return err
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

func output(cmd string, args ...string) (string, error) {
	out, err := exec.Command(cmd, args...).CombinedOutput()
	if err != nil {
		return "", errorf("%v: %v", out, err.Error())
	}
	return strings.TrimSpace(string(out)), nil
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

type deb struct {
	Name    string
	Path    string
	Version string
	Arch    string
}
