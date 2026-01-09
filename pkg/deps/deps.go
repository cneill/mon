package deps

import "strings"

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

func (d Dependencies) Diff(older Dependencies) string { //nolint:cyclop
	// TODO: colorize?
	// TODO: return the data, not a string?
	uniqueCurrent := map[string]Dependency{}
	for _, dep := range d {
		uniqueCurrent[dep.Package()] = dep
	}

	uniqueOlder := map[string]Dependency{}
	for _, dep := range older {
		uniqueOlder[dep.Package()] = dep
	}

	var added, removed, bumped []string

	// Find added and bumped packages
	for pkg, currentDep := range uniqueCurrent {
		olderDep, existed := uniqueOlder[pkg]
		if !existed {
			added = append(added, currentDep.String())
		} else if olderDep.Version != currentDep.Version {
			bumped = append(bumped, pkg+": "+olderDep.Version+" => "+currentDep.Version)
		}
	}

	// Find removed packages
	for pkg, olderDep := range uniqueOlder {
		if _, exists := uniqueCurrent[pkg]; !exists {
			removed = append(removed, olderDep.String())
		}
	}

	if len(added) == 0 && len(removed) == 0 && len(bumped) == 0 {
		return ""
	}

	builder := &strings.Builder{}

	if len(added) > 0 {
		builder.WriteString("  Added:\n")

		for _, pkg := range added {
			builder.WriteString("    + " + pkg + "\n")
		}
	}

	if len(removed) > 0 {
		builder.WriteString("  Removed:\n")

		for _, pkg := range removed {
			builder.WriteString("    - " + pkg + "\n")
		}
	}

	if len(bumped) > 0 {
		builder.WriteString("  Version changes:\n")

		for _, pkg := range bumped {
			builder.WriteString("    ~ " + pkg + "\n")
		}
	}

	return builder.String()
}
