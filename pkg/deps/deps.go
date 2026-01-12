package deps

type Dependency struct {
	Name    string
	URL     string
	Version string
}

func (d Dependency) Package() string {
	switch {
	case d.Name == "":
		return d.URL
	case d.URL == "":
		return d.Name
	case d.Name != "" && d.URL != "":
		return d.Name + " (" + d.URL + ")"
	default:
		return "<unknown>"
	}
}

func (d Dependency) String() string {
	return d.Package() + " @ " + d.Version
}

type Dependencies []Dependency

type UpdatedDependency struct {
	Initial Dependency
	Latest  Dependency
}

type UpdatedDependencies []UpdatedDependency

type FileDiff struct {
	Path                string
	NewDependencies     Dependencies
	DeletedDependencies Dependencies
	UpdatedDependencies UpdatedDependencies
}

func (f FileDiff) IsEmpty() bool {
	return len(f.NewDependencies) == 0 &&
		len(f.DeletedDependencies) == 0 &&
		len(f.UpdatedDependencies) == 0
}

type FileDiffs []FileDiff

func (f FileDiffs) AllEmpty() bool {
	for _, diff := range f {
		if !diff.IsEmpty() {
			return false
		}
	}

	return true
}

func (d Dependencies) Diff(name string, initial Dependencies) FileDiff {
	uniqueLatest := map[string]Dependency{}
	for _, dep := range d {
		uniqueLatest[dep.Package()] = dep
	}

	uniqueInitial := map[string]Dependency{}
	for _, dep := range initial {
		uniqueInitial[dep.Package()] = dep
	}

	var (
		added, removed Dependencies
		bumped         UpdatedDependencies
	)

	// Find added and bumped packages
	for pkg, latestDep := range uniqueLatest {
		initialDep, existed := uniqueInitial[pkg]
		if !existed {
			added = append(added, latestDep)
		} else if initialDep.Version != latestDep.Version {
			bumped = append(bumped, UpdatedDependency{
				Initial: initialDep,
				Latest:  latestDep,
			})
		}
	}

	// Find removed packages
	for pkg, initialDep := range uniqueInitial {
		if _, exists := uniqueLatest[pkg]; !exists {
			removed = append(removed, initialDep)
		}
	}

	return FileDiff{
		Path:                name,
		NewDependencies:     added,
		DeletedDependencies: removed,
		UpdatedDependencies: bumped,
	}
}
