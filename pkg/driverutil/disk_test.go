// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package driverutil

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lima-vm/go-qcow2reader"
	"github.com/lima-vm/go-qcow2reader/image"
	"gotest.tools/v3/assert"

	"github.com/lima-vm/lima/v2/pkg/iso9660util"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
	"github.com/lima-vm/lima/v2/pkg/osutil"
)

const (
	typeRAW  = image.Type("raw")
	typeASIF = image.Type("asif")
)

func writeMinimalISO(t *testing.T, path string) {
	t.Helper()
	entries := []iso9660util.Entry{
		{Path: "/hello.txt", Reader: strings.NewReader("hello world")},
	}
	assert.NilError(t, iso9660util.Write(path, "TESTISO", entries))
}

func writeNonISO(t *testing.T, path string) {
	t.Helper()
	size := 64 * 1024
	buf := make([]byte, size)
	copy(buf[0x8001:], "XXXXX")
	assert.NilError(t, os.WriteFile(path, buf, 0o644))
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	assert.NilError(t, err)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func detectImageType(t *testing.T, path string) image.Type {
	t.Helper()
	f, err := os.Open(path)
	assert.NilError(t, err)
	defer f.Close()
	img, err := qcow2reader.Open(f)
	assert.NilError(t, err)
	return img.Type()
}

func checkDisk(t *testing.T, diskPath string, expectedType image.Type) {
	t.Helper()
	fi, err := os.Stat(diskPath)
	assert.NilError(t, err)
	assert.Assert(t, fi.Size() > 0)
	assert.Equal(t, detectImageType(t, diskPath), expectedType)
}

func assertSymlink(t *testing.T, path, expectedTarget string) {
	t.Helper()
	target, err := os.Readlink(path)
	assert.NilError(t, err)
	assert.Equal(t, target, expectedTarget)
}

func isMacOS26OrHigher() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	version, err := osutil.ProductVersion()
	if err != nil {
		return false
	}
	return version.Major >= 26
}

func TestEnsureDisk_WithISOImage(t *testing.T) {
	instDir := t.TempDir()
	imagePath := filepath.Join(instDir, filenames.Image)
	diskPath := filepath.Join(instDir, filenames.Disk)
	isoPath := filepath.Join(instDir, filenames.ISO)

	formats := []image.Type{typeRAW}
	if isMacOS26OrHigher() {
		formats = append(formats, typeASIF)
	}

	for _, format := range formats {
		writeMinimalISO(t, imagePath)
		imageHashBefore := sha256File(t, imagePath)

		assert.NilError(t, EnsureDisk(t.Context(), instDir, "2MiB", format))

		// image should have been renamed to iso
		assert.Assert(t, !osutil.FileExists(imagePath))
		assert.Equal(t, imageHashBefore, sha256File(t, isoPath))
		isISO, err := iso9660util.IsISO9660(isoPath)
		assert.NilError(t, err)
		assert.Assert(t, isISO)

		// disk should be a real file (empty data disk)
		checkDisk(t, diskPath, format)

		assert.NilError(t, os.Remove(diskPath))
		assert.NilError(t, os.Remove(isoPath))
	}
}

func TestEnsureDisk_WithNonISOImage(t *testing.T) {
	instDir := t.TempDir()
	imagePath := filepath.Join(instDir, filenames.Image)
	diskPath := filepath.Join(instDir, filenames.Disk)

	formats := []image.Type{typeRAW}
	if isMacOS26OrHigher() {
		formats = append(formats, typeASIF)
	}

	for _, format := range formats {
		writeNonISO(t, imagePath)

		assert.NilError(t, EnsureDisk(t.Context(), instDir, "2MiB", format))

		// image should have been consumed
		assert.Assert(t, !osutil.FileExists(imagePath))

		// disk should be the converted image
		checkDisk(t, diskPath, format)

		assert.NilError(t, os.Remove(diskPath))
	}
}

func TestEnsureDisk_ExistingDisk(t *testing.T) {
	instDir := t.TempDir()
	imagePath := filepath.Join(instDir, filenames.Image)
	diskPath := filepath.Join(instDir, filenames.Disk)

	writeNonISO(t, imagePath)

	formats := []image.Type{typeRAW}
	if isMacOS26OrHigher() {
		formats = append(formats, typeASIF)
	}

	for _, format := range formats {
		assert.NilError(t, os.WriteFile(diskPath, []byte("preexisting"), 0o644))
		origHash := sha256File(t, diskPath)
		assert.NilError(t, EnsureDisk(t.Context(), instDir, "2MiB", format))
		assert.Equal(t, sha256File(t, diskPath), origHash)
		assert.NilError(t, os.Remove(diskPath))
	}
}

func TestEnsureDiskSymlink(t *testing.T) {
	instDir := t.TempDir()
	diskDir := filepath.Join(t.TempDir(), "disks", "vm1")
	instDiskPath := filepath.Join(instDir, filenames.Disk)
	realDiskPath := filepath.Join(diskDir, filenames.Disk)

	// An empty diskDir means "the default location", and must not create a symlink.
	assert.NilError(t, EnsureDiskSymlink(instDir, ""))
	assert.Assert(t, !osutil.IsSymlink(instDiskPath))

	assert.NilError(t, EnsureDiskSymlink(instDir, diskDir))
	assert.Assert(t, osutil.IsSymlink(instDiskPath))
	target, err := os.Readlink(instDiskPath)
	assert.NilError(t, err)
	assert.Equal(t, target, realDiskPath)
	// The disk file itself is created later, so the symlink may dangle for now.
	assert.Assert(t, !osutil.FileExists(instDiskPath))

	// Calling it again for the same directory is a NOP.
	assert.NilError(t, EnsureDiskSymlink(instDir, diskDir))

	// The disk file is created through the symlink.
	assert.NilError(t, os.WriteFile(instDiskPath, []byte("disk"), 0o644))
	b, err := os.ReadFile(realDiskPath)
	assert.NilError(t, err)
	assert.Equal(t, string(b), "disk")
}

func TestEnsureDiskSymlink_Errors(t *testing.T) {
	t.Run("inside instance directory", func(t *testing.T) {
		instDir := t.TempDir()
		err := EnsureDiskSymlink(instDir, filepath.Join(instDir, "disks"))
		assert.ErrorContains(t, err, "must be outside of the instance directory")
		assert.Assert(t, !osutil.IsSymlink(filepath.Join(instDir, filenames.Disk)))
	})

	t.Run("disk file already exists", func(t *testing.T) {
		instDir := t.TempDir()
		diskDir := t.TempDir()
		assert.NilError(t, os.WriteFile(filepath.Join(diskDir, filenames.Disk), []byte("other"), 0o644))
		err := EnsureDiskSymlink(instDir, diskDir)
		assert.ErrorContains(t, err, "already exists")
		assert.Assert(t, !osutil.IsSymlink(filepath.Join(instDir, filenames.Disk)))
	})

	t.Run("disk path is not a directory", func(t *testing.T) {
		instDir := t.TempDir()
		diskDir := filepath.Join(t.TempDir(), "disk.qcow2")
		assert.NilError(t, os.WriteFile(diskDir, []byte(""), 0o644))
		err := EnsureDiskSymlink(instDir, diskDir)
		assert.ErrorContains(t, err, "is not a directory")
	})

	t.Run("disk in instance directory is not a symlink", func(t *testing.T) {
		instDir := t.TempDir()
		assert.NilError(t, os.WriteFile(filepath.Join(instDir, filenames.Disk), []byte("legacy"), 0o644))
		err := EnsureDiskSymlink(instDir, t.TempDir())
		assert.ErrorContains(t, err, "is not a symlink")
	})
}

func TestEnsureDisk_WithSymlink(t *testing.T) {
	instDir := t.TempDir()
	diskDir := t.TempDir()
	diskPath := filepath.Join(instDir, filenames.Disk)
	realDiskPath := filepath.Join(diskDir, filenames.Disk)

	assert.NilError(t, EnsureDiskSymlink(instDir, diskDir))
	writeNonISO(t, filepath.Join(instDir, filenames.Image))

	assert.NilError(t, EnsureDisk(t.Context(), instDir, "2MiB", typeRAW))

	// `disk` must still be the symlink, and the disk must be created at its target.
	assert.Assert(t, osutil.IsSymlink(diskPath))
	target, err := os.Readlink(diskPath)
	assert.NilError(t, err)
	assert.Equal(t, target, realDiskPath)
	checkDisk(t, realDiskPath, typeRAW)
}

func TestMigrateDiskLayout_LegacyDiffDisk(t *testing.T) {
	instDir := t.TempDir()
	diffDiskPath := filepath.Join(instDir, filenames.DiffDiskLegacy)

	assert.NilError(t, os.WriteFile(diffDiskPath, []byte("legacy-disk"), 0o644))
	origHash := sha256File(t, diffDiskPath)

	assert.NilError(t, MigrateDiskLayout(instDir))

	// disk should be a symlink to diffdisk
	diskPath := filepath.Join(instDir, filenames.Disk)
	assertSymlink(t, diskPath, filenames.DiffDiskLegacy)
	assert.Equal(t, sha256File(t, diskPath), origHash)

	// diffdisk should still exist (untouched)
	assert.Assert(t, osutil.FileExists(diffDiskPath))
}

func TestMigrateDiskLayout_LegacyISOBaseDisk(t *testing.T) {
	instDir := t.TempDir()
	baseDiskPath := filepath.Join(instDir, filenames.BaseDiskLegacy)
	diffDiskPath := filepath.Join(instDir, filenames.DiffDiskLegacy)

	writeMinimalISO(t, baseDiskPath)
	baseHash := sha256File(t, baseDiskPath)
	assert.NilError(t, os.WriteFile(diffDiskPath, []byte("legacy-disk"), 0o644))
	diffHash := sha256File(t, diffDiskPath)

	assert.NilError(t, MigrateDiskLayout(instDir))

	// disk should be a symlink to diffdisk
	diskPath := filepath.Join(instDir, filenames.Disk)
	assertSymlink(t, diskPath, filenames.DiffDiskLegacy)
	assert.Equal(t, sha256File(t, diskPath), diffHash)

	// iso should be a symlink to basedisk
	isoPath := filepath.Join(instDir, filenames.ISO)
	assertSymlink(t, isoPath, filenames.BaseDiskLegacy)
	assert.Equal(t, sha256File(t, isoPath), baseHash)

	// original files should still exist
	assert.Assert(t, osutil.FileExists(diffDiskPath))
	assert.Assert(t, osutil.FileExists(baseDiskPath))
}

func TestMigrateDiskLayout_LegacyNonISOBaseDisk(t *testing.T) {
	instDir := t.TempDir()
	baseDiskPath := filepath.Join(instDir, filenames.BaseDiskLegacy)
	diffDiskPath := filepath.Join(instDir, filenames.DiffDiskLegacy)

	writeNonISO(t, baseDiskPath)
	baseHash := sha256File(t, baseDiskPath)
	assert.NilError(t, os.WriteFile(diffDiskPath, []byte("legacy-disk"), 0o644))

	assert.NilError(t, MigrateDiskLayout(instDir))

	// disk should be a symlink to diffdisk
	diskPath := filepath.Join(instDir, filenames.Disk)
	assertSymlink(t, diskPath, filenames.DiffDiskLegacy)

	// non-ISO basedisk should remain unchanged (qcow2 backing file)
	assert.Equal(t, sha256File(t, baseDiskPath), baseHash)

	// no iso symlink should be created
	isoPath := filepath.Join(instDir, filenames.ISO)
	_, err := os.Lstat(isoPath)
	assert.Assert(t, os.IsNotExist(err))
}

func TestMigrateDiskLayout_AlreadyMigrated(t *testing.T) {
	instDir := t.TempDir()
	diskPath := filepath.Join(instDir, filenames.Disk)

	assert.NilError(t, os.WriteFile(diskPath, []byte("current-disk"), 0o644))
	origHash := sha256File(t, diskPath)

	assert.NilError(t, MigrateDiskLayout(instDir))

	// disk should be unchanged
	assert.Equal(t, sha256File(t, diskPath), origHash)
}
