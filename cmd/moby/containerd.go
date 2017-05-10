package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	log "github.com/Sirupsen/logrus"
)

// initial implementation using exec
// we should probably import containerd code directly and create a containerd
// and a dist ourselves, connected with a socket so the user does not have to
// install these commands at all.

const toml string = `
state = "%s/state"
root = "%s/root"
snapshotter = "%s"
subreaper = false
oom_score = 0

[grpc]
  address = "%s/containerd.sock"
  uid = -1
  gid = -1

[debug]
  address = "%s/debug.sock"
  level = "info"

[metrics]
  address = ""
`

type ctd struct {
	cmd      *exec.Cmd
	w        io.Writer
	dir      string
	shutdown bool
}

// NewContainerd returns a new instance of containerd running in an isolated directory
func NewContainerd(w io.Writer) (*ctd, error) {
	dir, err := ioutil.TempDir("", "moby-ctd")
	if err != nil {
		return nil, err
	}

	snapshotter := "overlay"
	if runtime.GOOS == "darwin" {
		snapshotter = "naive"
	}

	config := fmt.Sprintf(toml, dir, dir, snapshotter, dir, dir)
	configPath := filepath.Join(dir, "config.toml")

	err = ioutil.WriteFile(configPath, []byte(config), 0600)
	if err != nil {
		return nil, err
	}

	containerd, err := exec.LookPath("containerd")
	if err != nil {
		return nil, errors.New("Cannot find containerd in path")
	}
	cmd := exec.Command(containerd, "--config", configPath)
	cmd.Stderr = w

	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	ctd := new(ctd)
	ctd.cmd = cmd
	ctd.w = w
	ctd.dir = dir

	return ctd, nil
}

// Kill kills containerd but does not remove the directory.
// You can call Close() after to clean up.
func (ctd *ctd) Kill() error {
	err := ctd.cmd.Process.Kill()
	if err != nil {
		return err
	}
	// TODO check if it died rather than was killed
	_ = ctd.cmd.Wait()

	ctd.shutdown = true

	return nil
}

// Close removes containerd and the directory
func (ctd *ctd) Close() error {
	if !ctd.shutdown {
		err := ctd.Kill()
		if err != nil {
			return err
		}
	}

	err := os.RemoveAll(ctd.dir)
	if err != nil {
		return err
	}

	return nil
}

// ImageName takes a Docker style repo name and returns a fully qualified name
// eg "alpine" will return "docker.io/library/alpine:latest"
func ImageName(image string) string {
	slash := strings.SplitN(image, "/", 3)
	switch len(slash) {
	case 1:
		image = "docker.io/library/" + image
	case 2:
		image = "docker.io/" + image
	}

	colon := strings.SplitN(image, ":", 2)
	if len(colon) != 2 {
		image = image + ":latest"
	}

	return image
}

// Fetch fetches an image into the content store
func (ctd *ctd) Fetch(repo string, trust bool) error {
	log.Debugf("fetch: %s", repo)
	dist, err := exec.LookPath("dist")
	if err != nil {
		return errors.New("Cannot find dist in path")
	}

	root := filepath.Join(ctd.dir, "root")
	address := filepath.Join(ctd.dir, "containerd.sock")
	cmd := exec.Command(dist, "--address", address, "--root", root, "fetch", ImageName(repo))
	cmd.Stderr = ctd.w
	cmd.Stdout = ctd.w
	err = cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

// Store returns a blob containing a tarball of the content store
func (ctd *ctd) Store(w io.Writer) error {
	const prefix = "var/lib/containerd/"

	tw := tar.NewWriter(w)
	defer tw.Close()

	src := filepath.Join(ctd.dir, "root")

	err := tarPrefix(prefix, tw)
	if err != nil {
		return err
	}

	return filepath.Walk(src, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		hdr, err := tar.FileInfoHeader(fi, fi.Name())
		if err != nil {
			return err
		}

		// remove temporary path
		hdr.Name = strings.TrimPrefix(strings.Replace(file, src, "", -1), string(filepath.Separator))
		// add correct path
		hdr.Name = filepath.Join(prefix, hdr.Name)

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeSymlink, tar.TypeLink:
			f, err := os.Open(file)
			defer f.Close()
			if err != nil {
				return err
			}
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
			return nil
		default:
			return nil
		}
	})
}

// Bundle outputs an image name and config file, and adds the image to the image store
func (ctd *ctd) Bundle(path string, image string, config []byte, trust bool) ([]byte, error) {
	log.Debugf("bundle: %s %s cfg: %s", path, image, string(config))
	out := new(bytes.Buffer)
	tw := tar.NewWriter(out)
	err := tarPrefix(path+"/", tw)
	if err != nil {
		return []byte{}, err
	}
	hdr := &tar.Header{
		Name: path + "/" + "config.json",
		Mode: 0644,
		Size: int64(len(config)),
	}
	err = tw.WriteHeader(hdr)
	if err != nil {
		return []byte{}, err
	}
	buf := bytes.NewBuffer(config)
	_, err = io.Copy(tw, buf)
	if err != nil {
		return []byte{}, err
	}
	hdr = &tar.Header{
		Name: path + "/" + "image",
		Mode: 0644,
		Size: int64(len(image)),
	}
	err = tw.WriteHeader(hdr)
	if err != nil {
		return []byte{}, err
	}
	buf = bytes.NewBufferString(image)
	_, err = io.Copy(tw, buf)
	if err != nil {
		return []byte{}, err
	}
	err = tw.Close()
	if err != nil {
		return []byte{}, err
	}
	err = ctd.Fetch(image, trust)
	if err != nil {
		return []byte{}, err
	}
	return out.Bytes(), nil
}
