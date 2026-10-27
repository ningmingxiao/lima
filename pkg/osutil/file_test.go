// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package osutil

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestResolveSymlink(t *testing.T) {
	dir := t.TempDir()

	// A non-symlink is returned as is.
	regular := filepath.Join(dir, "regular")
	assert.NilError(t, os.WriteFile(regular, []byte(""), 0o644))
	assert.Equal(t, ResolveSymlink(regular), regular)
	assert.Equal(t, ResolveSymlink(filepath.Join(dir, "nonexistent")), filepath.Join(dir, "nonexistent"))

	// A symlink to an existing file is resolved.
	target := filepath.Join(dir, "target")
	assert.NilError(t, os.WriteFile(target, []byte(""), 0o644))
	link := filepath.Join(dir, "link")
	assert.NilError(t, os.Symlink(target, link))
	assert.Equal(t, ResolveSymlink(link), target)

	// A dangling symlink is resolved too, so that the target can be created.
	dangling := filepath.Join(dir, "dangling")
	danglingTarget := filepath.Join(dir, "not-yet-created")
	assert.NilError(t, os.Symlink(danglingTarget, dangling))
	assert.Equal(t, ResolveSymlink(dangling), danglingTarget)

	// A dangling relative symlink is resolved relative to the symlink's directory.
	sub := filepath.Join(dir, "sub")
	assert.NilError(t, os.MkdirAll(sub, 0o700))
	relDangling := filepath.Join(sub, "rel-dangling")
	assert.NilError(t, os.Symlink("rel-target", relDangling))
	assert.Equal(t, ResolveSymlink(relDangling), filepath.Join(sub, "rel-target"))
}

func TestWriteFileBeneathDir(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "mnt")
	assert.NilError(t, os.MkdirAll(dir, 0o700))

	// A regular destination is written normally.
	regular := filepath.Join(dir, "config")
	assert.NilError(t, WriteFileBeneathDir(regular, []byte("ok\n"), 0o600))
	got, err := os.ReadFile(regular)
	assert.NilError(t, err)
	assert.Equal(t, string(got), "ok\n")

	// A symlink at the destination pointing outside the parent directory is
	// refused, and the link target is left untouched.
	secret := filepath.Join(base, "authorized_keys")
	assert.NilError(t, os.WriteFile(secret, []byte("ORIGINAL\n"), 0o600))
	linked := filepath.Join(dir, "linked")
	assert.NilError(t, os.Symlink(secret, linked))

	err = WriteFileBeneathDir(linked, []byte("ATTACKER\n"), 0o600)
	assert.Assert(t, err != nil, "expected the symlinked write to be refused")
	got, err = os.ReadFile(secret)
	assert.NilError(t, err)
	assert.Equal(t, string(got), "ORIGINAL\n")
}

func TestRemoveStaleSocket(t *testing.T) {
	base := t.TempDir()

	// A non-existent path is not an error.
	assert.NilError(t, RemoveStaleSocket(filepath.Join(base, "absent")))

	// A regular file at the socket path is left in place, not deleted.
	file := filepath.Join(base, "file")
	assert.NilError(t, os.WriteFile(file, []byte("keep\n"), 0o600))
	assert.Assert(t, RemoveStaleSocket(file) != nil, "expected a regular file to be refused")
	assert.Assert(t, FileExists(file), "regular file must not be removed")

	// A directory at the socket path is left in place, not deleted recursively.
	dir := filepath.Join(base, "dir")
	assert.NilError(t, os.MkdirAll(dir, 0o700))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "child"), []byte("keep\n"), 0o600))
	assert.Assert(t, RemoveStaleSocket(dir) != nil, "expected a directory to be refused")
	assert.Assert(t, FileExists(filepath.Join(dir, "child")), "directory contents must not be removed")

	// An actual stale socket is removed so a new listener can bind.
	sock := filepath.Join(base, "sock")
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "unix", sock)
	assert.NilError(t, err)
	defer l.Close()
	assert.NilError(t, RemoveStaleSocket(sock))
	assert.Assert(t, !FileExists(sock), "stale socket must be removed")
}
