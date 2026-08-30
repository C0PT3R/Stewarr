package httpui

import (
	"context"
	"os"
	"path/filepath"
	"syscall"

	"connarr/internal/removal"
)

func selectedUnmanagedStates(plan removal.RemovalPlan, paths []string) []removal.FileState {
	wanted := map[string]bool{}
	for _, p := range paths {
		wanted[filepath.Clean(p)] = true
	}
	out := []removal.FileState{}
	for _, f := range plan.Files {
		if f.Owner == removal.UnmanagedOwner && f.Selected && wanted[filepath.Clean(f.Path)] {
			out = append(out, f)
		}
	}
	return out
}

// unlinkVerified closes the last practical time-of-check gap before direct OS
// deletion. Each File is checked independently against live owners and the
// physical identity displayed by the confirmed plan.
func (server *Server) unlinkVerified(ctx context.Context, expected []removal.FileState) ([]string, []string) {
	results, errs := []string{}, []string{}
	for _, state := range expected {
		p := filepath.Clean(state.Path)
		if err := server.inv.VerifyUnmanagedContext(ctx, []string{p}); err != nil {
			errs = append(errs, p+": final ownership verification failed: "+err.Error())
			continue
		}
		info, err := os.Lstat(p)
		if err != nil {
			errs = append(errs, p+": final identity verification failed: "+err.Error())
			continue
		}
		if !info.Mode().IsRegular() || !state.Exists || !state.IdentityKnown {
			errs = append(errs, p+": final identity verification failed: path is not the confirmed regular File")
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || uint64(stat.Dev) != state.Device || uint64(stat.Ino) != state.Inode {
			errs = append(errs, p+": final identity verification failed: physical File changed after confirmation")
			continue
		}
		if err := os.Remove(p); err != nil {
			errs = append(errs, p+": "+err.Error())
		} else {
			results = append(results, "filesystem removed: "+p)
		}
	}
	return results, errs
}
