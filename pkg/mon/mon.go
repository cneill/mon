package mon

import "fmt"

type Opts struct {
	GitWatch   bool
	NoColor    bool
	ProjectDir string
}

func (o *Opts) OK() error {
	if o.ProjectDir == "" {
		return fmt.Errorf("must supply project dir")
	}

	return nil
}

type Mon struct {
	*Opts
}

func New(opts *Opts) (*Mon, error) {
	if err := opts.OK(); err != nil {
		return nil, fmt.Errorf("failed to configure mon: %w", err)
	}

	mon := &Mon{
		Opts: opts,
	}

	return mon, nil
}

func (m *Mon) Run() error {
	return nil
}
