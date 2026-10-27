// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package instance

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
	"github.com/lima-vm/lima/v2/pkg/osutil"
)

func TestRemoveDiskTarget(t *testing.T) {
	t.Run("no symlink", func(t *testing.T) {
		instDir := t.TempDir()
		diskPath := filepath.Join(instDir, filenames.Disk)
		assert.NilError(t, os.WriteFile(diskPath, []byte("disk"), 0o644))
		assert.NilError(t, removeDiskTarget(instDir))
		// The disk file inside the instance directory is removed together with
		// the instance directory, not here.
		assert.Assert(t, osutil.FileExists(diskPath))
	})

	t.Run("symlink outside of the instance directory", func(t *testing.T) {
		instDir := t.TempDir()
		realDiskPath := filepath.Join(t.TempDir(), filenames.Disk)
		assert.NilError(t, os.WriteFile(realDiskPath, []byte("disk"), 0o644))
		assert.NilError(t, os.Symlink(realDiskPath, filepath.Join(instDir, filenames.Disk)))

		assert.NilError(t, removeDiskTarget(instDir))
		assert.Assert(t, !osutil.FileExists(realDiskPath))
	})

	t.Run("dangling symlink outside of the instance directory", func(t *testing.T) {
		instDir := t.TempDir()
		realDiskPath := filepath.Join(t.TempDir(), filenames.Disk)
		assert.NilError(t, os.Symlink(realDiskPath, filepath.Join(instDir, filenames.Disk)))
		assert.NilError(t, removeDiskTarget(instDir))
	})

	t.Run("symlink inside of the instance directory", func(t *testing.T) {
		// Instances migrated from the legacy layout have `disk` as a symlink to
		// `diffdisk`; the target is removed together with the instance directory.
		instDir := t.TempDir()
		diffDiskPath := filepath.Join(instDir, filenames.DiffDiskLegacy)
		assert.NilError(t, os.WriteFile(diffDiskPath, []byte("disk"), 0o644))
		assert.NilError(t, os.Symlink(filenames.DiffDiskLegacy, filepath.Join(instDir, filenames.Disk)))

		assert.NilError(t, removeDiskTarget(instDir))
		assert.Assert(t, osutil.FileExists(diffDiskPath))
	})

	t.Run("symlink outside of the instance directory with a vhdx hardlink", func(t *testing.T) {
		// The HCS driver attaches the disk as a hardlink named `disk.vhdx` that
		// lives next to the disk file, i.e. outside of the instance directory
		// when `--disk-path` is used.
		instDir := t.TempDir()
		realDiskPath := filepath.Join(t.TempDir(), filenames.Disk)
		assert.NilError(t, os.WriteFile(realDiskPath, []byte("disk"), 0o644))
		vhdxPath := realDiskPath + ".vhdx"
		assert.NilError(t, os.Link(realDiskPath, vhdxPath))
		assert.NilError(t, os.Symlink(realDiskPath, filepath.Join(instDir, filenames.Disk)))

		assert.NilError(t, removeDiskTarget(instDir))
		assert.Assert(t, !osutil.FileExists(realDiskPath))
		assert.Assert(t, !osutil.FileExists(vhdxPath))
	})

	t.Run("symlink outside of the instance directory with an unrelated vhdx file", func(t *testing.T) {
		instDir := t.TempDir()
		realDiskPath := filepath.Join(t.TempDir(), filenames.Disk)
		assert.NilError(t, os.WriteFile(realDiskPath, []byte("disk"), 0o644))
		vhdxPath := realDiskPath + ".vhdx"
		assert.NilError(t, os.WriteFile(vhdxPath, []byte("not a link"), 0o644))
		assert.NilError(t, os.Symlink(realDiskPath, filepath.Join(instDir, filenames.Disk)))

		assert.NilError(t, removeDiskTarget(instDir))
		assert.Assert(t, !osutil.FileExists(realDiskPath))
		assert.Assert(t, osutil.FileExists(vhdxPath))
	})
}
