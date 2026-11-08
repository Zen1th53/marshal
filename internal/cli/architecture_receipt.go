package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/store"
)

// architectureReceipt imports an externally signed receipt under the compiled
// edition's provenance rules. It neither enrolls nor signs an issuer receipt.
func (c command) architectureReceipt(ctx context.Context, args []string) error {
	if len(args) != 3 || args[0] != "receipt" || args[1] != "import" || args[2] == "" {
		return fmt.Errorf("%w: usage: architecture receipt import FILE", model.ErrInvalid)
	}
	layout, err := project.Discover(c.root)
	if err != nil {
		return err
	}
	f, err := os.Open(args[2])
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 128<<10 {
		return model.ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(f, (128<<10)+1))
	if err != nil {
		return err
	}
	if len(raw) > 128<<10 {
		return model.ErrInvalid
	}
	r, err := architecturecodec.DecodeSignedArchitectureReceipt(raw)
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, layout.Database)
	if err != nil {
		return err
	}
	defer s.Close()
	a, err := s.ImportObservedArchitectureReceipt(ctx, r)
	if err != nil {
		return err
	}
	return c.print(a, fmt.Sprintf("Architecture receipt %s accepted for revision %d", a.ReceiptID, a.Revision))
}
