package buildingenergy

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
)

var ErrInvalidClassification = errors.New("invalid classified usage input")

const classificationRuleVersion = "m1-c-v1"

type CategoryNode struct {
	ID       string
	ParentID string
	Name     string
	Energy   EnergyType
}

type CategoryAllocation struct {
	CategoryID string
	WeightPPM  int
}

// CategoryBinding assigns one metering point to leaf categories during an effective period.
type CategoryBinding struct {
	Point       PointRef
	Meta        VersionMeta
	Allocations []CategoryAllocation
}

type ClassificationSnapshot struct {
	Project  ProjectKey
	Meta     VersionMeta
	Nodes    []CategoryNode
	Bindings []CategoryBinding
	// ProportionalAcrossObjects is an explicit policy in this catalog version.
	// It permits multiplying a partial object share by partial category shares.
	ProportionalAcrossObjects bool
}

// ClassificationCatalog supplies an authoritative, versioned project taxonomy.
type ClassificationCatalog interface {
	ReadClassification(context.Context, ProjectKey, string, Range) (ClassificationSnapshot, error)
}

type CategoryBoundaryContributor struct {
	BoundaryContributor
	MappingGeneration uint64
	Channel           SourceRef
}

// CategoryTopologySnapshot contains independently verified, mutually disjoint
// physical boundaries. Each group selects a total or disjoint submeters.
type CategoryTopologySnapshot struct {
	Project    ProjectKey
	Meta       VersionMeta
	Boundaries [][]CategoryBoundaryContributor
}

// CategoryTopologySource certifies physical relationships between selected
// channels, including groups whose distinct point IDs could measure the same load.
type CategoryTopologySource interface {
	ReadCategoryTopology(context.Context, ProjectKey, string, Range) (CategoryTopologySnapshot, error)
}

type ClassifiedUsageQuery struct {
	Project ProjectKey
	Window  Window
	Object  ObjectKey // Zero selects the full authorized project boundary.
}

type CategoryPointResult struct {
	Result  Result
	Mapping MappingTimeline
}

// WeightedCoverage counts fractions of atomic observations. A split observation
// is counted once in total and credited to each category by its assigned share.
type WeightedCoverage struct {
	Expected float64
	Valid    float64
	Missing  float64
	Suspect  float64
}

type CategoryValue struct {
	Usage    *float64
	Coverage WeightedCoverage
}

type CategoryRow struct {
	Category CategoryNode
	CategoryValue
	Children []CategoryRow
}

type EnergyCategorySummary struct {
	Energy       EnergyType
	Unit         string
	Total        CategoryValue
	Unclassified CategoryValue
	Categories   []CategoryRow
}

// ClassifiedIntervalEvidence retains the normalized usage within the query
// scope. Allocations name leaves and express fractions of that scoped usage.
type ClassifiedIntervalEvidence struct {
	Point                  PointRef
	Energy                 EnergyType
	Unit                   string
	MappingGeneration      uint64
	MeterRuleVersion       string
	ProfileVersion         string
	ObjectBindingVersion   string
	CategoryBindingVersion string
	Allocations            []CategoryAllocation
	Contributions          []CategoryContribution
	UnclassifiedUsage      *float64
	Trace                  IntervalTrace
}

// CategoryContribution records the normalized, scoped leaf quantity used in
// the aggregate. A nil quantity preserves a missing or suspect interval.
type CategoryContribution struct {
	CategoryID string
	Usage      *float64
}

type ClassifiedUsageReport struct {
	Query           ClassifiedUsageQuery
	RuleVersion     string
	CatalogVersion  VersionMeta
	TopologyVersion VersionMeta
	Energy          []EnergyCategorySummary
	Intervals       []ClassifiedIntervalEvidence
}

type categoryTree struct {
	nodes    map[string]CategoryNode
	children map[string][]string
	order    []string
}

func buildCategoryTree(nodes []CategoryNode) (categoryTree, error) {
	tree := categoryTree{
		nodes:    make(map[string]CategoryNode, len(nodes)),
		children: make(map[string][]string),
	}
	if len(nodes) == 0 {
		return tree, fmt.Errorf("%w: empty category tree", ErrInvalidClassification)
	}
	for _, node := range nodes {
		switch node.Energy {
		case Electricity, Water, Gas, Cooling, Heat, Steam:
		default:
			return tree, fmt.Errorf("%w: category %q has unknown energy", ErrInvalidClassification, node.ID)
		}
		if !named(node.ID) || !named(node.Name) || tree.nodes[node.ID].ID != "" {
			return tree, fmt.Errorf("%w: unnamed or duplicate category %q", ErrInvalidClassification, node.ID)
		}
		tree.nodes[node.ID] = node
		tree.children[node.ParentID] = append(tree.children[node.ParentID], node.ID)
	}
	for _, node := range nodes {
		if node.ParentID != "" && tree.nodes[node.ParentID].Energy != node.Energy {
			return tree, fmt.Errorf("%w: unresolved or different-energy parent of %q", ErrInvalidClassification, node.ID)
		}
	}
	for _, children := range tree.children {
		slices.Sort(children)
	}
	stack := slices.Clone(tree.children[""])
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		tree.order = append(tree.order, id)
		stack = append(stack, tree.children[id]...)
	}
	if len(tree.order) != len(nodes) {
		return tree, fmt.Errorf("%w: category parent cycle", ErrInvalidClassification)
	}
	return tree, nil
}

func indexCategoryBindings(project ProjectKey, tree categoryTree, bindings []CategoryBinding) (map[PointRef][]CategoryBinding, error) {
	index := make(map[PointRef][]CategoryBinding)
	for _, binding := range bindings {
		if binding.Point.Project != project || !binding.Point.Key.valid() {
			return nil, fmt.Errorf("%w: category binding point is outside the project", ErrInvalidClassification)
		}
		remaining := FullWeightPPM
		seen := make(map[string]bool, len(binding.Allocations))
		for _, allocation := range binding.Allocations {
			id, weight := allocation.CategoryID, allocation.WeightPPM
			if tree.nodes[id].ID == "" || len(tree.children[id]) != 0 || seen[id] || weight <= 0 || weight > remaining {
				return nil, fmt.Errorf("%w: invalid, duplicate or overweight leaf allocation %q", ErrInvalidClassification, id)
			}
			seen[id] = true
			remaining -= weight
		}
		binding.Allocations = slices.Clone(binding.Allocations)
		slices.SortFunc(binding.Allocations, func(a, b CategoryAllocation) int { return cmp.Compare(a.CategoryID, b.CategoryID) })
		index[binding.Point] = append(index[binding.Point], binding)
	}
	for _, bindings := range index {
		versions := make([]VersionMeta, len(bindings))
		for i, binding := range bindings {
			versions[i] = binding.Meta
		}
		if err := validateTimeline(versions); err != nil {
			return nil, fmt.Errorf("%w: category binding timeline: %w", ErrInvalidClassification, err)
		}
	}
	return index, nil
}

func categoryContributors(topology CategoryTopologySnapshot) (map[PointRef]CategoryBoundaryContributor, error) {
	if len(topology.Boundaries) == 0 {
		return nil, fmt.Errorf("%w: no authoritative boundary selection", ErrInvalidClassification)
	}
	points := make(map[PointRef]CategoryBoundaryContributor)
	channels := make(map[SourceRef]bool)
	for _, group := range topology.Boundaries {
		boundary := make([]BoundaryContributor, 0, len(group))
		for _, item := range group {
			if _, duplicate := points[item.Point]; duplicate || channels[item.Channel] ||
				item.MappingGeneration == 0 || !item.Channel.valid() {
				return nil, fmt.Errorf("%w: duplicate point/channel or missing topology evidence", ErrInvalidClassification)
			}
			points[item.Point], channels[item.Channel] = item, true
			boundary = append(boundary, item.BoundaryContributor)
		}
		if err := ValidateBoundaryContributors(topology.Project, boundary); err != nil {
			return nil, fmt.Errorf("%w: invalid physical contributor group: %w", ErrInvalidClassification, err)
		}
	}
	return points, nil
}

// CalculateClassifiedUsage consumes an authorized topology selection and one
// M1-A result per point over a common window. Callers authorize usage and the
// selected scope before supplying data. The calculator performs no I/O.
func CalculateClassifiedUsage(query ClassifiedUsageQuery, catalog ClassificationSnapshot, topology CategoryTopologySnapshot, inputs []CategoryPointResult) (ClassifiedUsageReport, error) {
	window, err := localWindow(query.Window)
	if err != nil {
		return ClassifiedUsageReport{}, err
	}
	query.Window = window
	if !query.Project.valid() || (query.Object != (ObjectKey{}) && !query.Object.valid()) ||
		catalog.Project != query.Project || topology.Project != query.Project ||
		!catalog.Meta.covers(window.Range) || !topology.Meta.covers(window.Range) {
		return ClassifiedUsageReport{}, fmt.Errorf("%w: project, object or snapshot coverage mismatch", ErrInvalidClassification)
	}
	tree, err := buildCategoryTree(catalog.Nodes)
	if err != nil {
		return ClassifiedUsageReport{}, err
	}
	bindings, err := indexCategoryBindings(query.Project, tree, catalog.Bindings)
	if err != nil {
		return ClassifiedUsageReport{}, err
	}
	contributors, err := categoryContributors(topology)
	if err != nil {
		return ClassifiedUsageReport{}, err
	}
	if len(inputs) != len(contributors) {
		return ClassifiedUsageReport{}, fmt.Errorf("%w: expected one complete interval result per selected point", ErrInvalidClassification)
	}

	points := slices.Clone(inputs)
	slices.SortFunc(points, func(a, b CategoryPointResult) int {
		return cmp.Or(cmp.Compare(a.Mapping.Point.Key.TerminalID, b.Mapping.Point.Key.TerminalID),
			cmp.Compare(a.Mapping.Point.Key.ItemCode, b.Mapping.Point.Key.ItemCode))
	})
	byEnergy := make(map[EnergyType]*categoryAccumulator)
	report := ClassifiedUsageReport{Query: query, RuleVersion: classificationRuleVersion, CatalogVersion: catalog.Meta, TopologyVersion: topology.Meta}
	for _, point := range points {
		key := point.Mapping.Point
		selected, exists := contributors[key]
		if !exists || selected.MappingGeneration != point.Mapping.Generation {
			return ClassifiedUsageReport{}, fmt.Errorf("%w: duplicate, unselected, or remapped point", ErrInvalidClassification)
		}
		delete(contributors, key)
		channel, err := validateCategoryPoint(point, window)
		if err != nil {
			return ClassifiedUsageReport{}, err
		}
		if channel != selected.Channel {
			return ClassifiedUsageReport{}, fmt.Errorf("%w: physical channel differs from the topology snapshot", ErrInvalidClassification)
		}
		energy := point.Result.Profile.Energy
		summary := byEnergy[energy]
		if summary == nil {
			summary = &categoryAccumulator{unit: point.Result.Unit, nodes: make(map[string]*CategoryValue)}
			byEnergy[energy] = summary
		}
		if err := summary.addPoint(tree, query.Object, point, bindings[key], catalog.ProportionalAcrossObjects, &report.Intervals); err != nil {
			return ClassifiedUsageReport{}, err
		}
	}
	for energy, summary := range byEnergy {
		if summary.total.Coverage.Expected == 0 {
			continue
		}
		row := EnergyCategorySummary{Energy: energy, Unit: summary.unit, Total: summary.total, Unclassified: summary.unclassified}
		row.Categories, err = summary.rows(tree, energy)
		if err != nil {
			return ClassifiedUsageReport{}, err
		}
		report.Energy = append(report.Energy, row)
	}
	slices.SortFunc(report.Energy, func(a, b EnergyCategorySummary) int { return cmp.Compare(a.Energy, b.Energy) })
	return report, nil
}

func validateCategoryPoint(point CategoryPointResult, window Window) (SourceRef, error) {
	result, mapping := point.Result, point.Mapping
	if result.Profile.Point != mapping.Point || result.RuleVersion != calculationVersion ||
		!sameRange(result.Window.Range, window.Range) || result.Window.Timezone != window.Timezone ||
		result.Window.Granularity != window.Granularity || !result.Window.AsOf.Equal(window.AsOf) {
		return SourceRef{}, fmt.Errorf("%w: usage window, cutoff or point does not match", ErrInvalidClassification)
	}
	if err := mapping.Validate(); err != nil {
		return SourceRef{}, err
	}
	profile := result.Profile
	unit, _, err := meteringScale(profile)
	if err != nil || unit != result.Unit || (profile.Kind != Cumulative && profile.Kind != Interval) {
		return SourceRef{}, fmt.Errorf("%w: unnormalized unit or unsupported measurement", ErrInvalidClassification)
	}
	var channel SourceRef
	for _, revision := range mapping.Profiles {
		approved := revision.Profile
		if approved.Meta.covers(window.Range) && sameCategoryProfile(approved, profile) {
			channel = revision.Channel
			break
		}
	}
	if !channel.valid() {
		return SourceRef{}, fmt.Errorf("%w: usage profile does not match its approved mapping version", ErrInvalidClassification)
	}
	t := window.From
	for _, trace := range result.Intervals {
		if !trace.From.Equal(t) || !trace.To.Equal(t.Add(profile.Cadence)) || trace.To.After(window.To) ||
			!trace.Quality.valid() ||
			(trace.Usage != nil && (!trace.Quality.usable() || !finite(*trace.Usage) || *trace.Usage < 0)) ||
			(trace.Usage == nil && trace.Quality.usable()) {
			return SourceRef{}, fmt.Errorf("%w: invalid atomic interval or quality on %v", ErrInvalidClassification, mapping.Point.Key)
		}
		t = trace.To
	}
	if !t.Equal(window.To) {
		return SourceRef{}, fmt.Errorf("%w: incomplete interval sequence for selected point", ErrInvalidClassification)
	}
	return channel, nil
}

func sameCategoryProfile(a, b PointProfile) bool {
	if a.Meta.Version != b.Meta.Version || a.Meta.Source != b.Meta.Source || !sameRange(a.Meta.Effective, b.Meta.Effective) {
		return false
	}
	a.Meta, b.Meta = VersionMeta{}, VersionMeta{}
	return a == b
}

type categoryAccumulator struct {
	unit         string
	total        CategoryValue
	unclassified CategoryValue
	nodes        map[string]*CategoryValue
}

func (a *categoryAccumulator) addPoint(tree categoryTree, object ObjectKey, point CategoryPointResult, bindings []CategoryBinding, proportional bool, intervals *[]ClassifiedIntervalEvidence) error {
	for _, trace := range point.Result.Intervals {
		evidence := ClassifiedIntervalEvidence{
			Point: point.Mapping.Point, Energy: point.Result.Profile.Energy, Unit: point.Result.Unit,
			MappingGeneration: point.Mapping.Generation, MeterRuleVersion: point.Result.RuleVersion, ProfileVersion: point.Result.Profile.Meta.Version,
			Trace: trace,
		}
		scopePPM := FullWeightPPM
		if object != (ObjectKey{}) {
			resolved, err := point.Mapping.resolve(object.Kind, trace.Range)
			if err != nil {
				return err
			}
			evidence.ObjectBindingVersion = resolved.BindingVersion.Version
			scopePPM = 0
			for _, allocation := range resolved.Allocations {
				if allocation.Target == object {
					scopePPM = allocation.WeightPPM
					break
				}
			}
		}
		if scopePPM == 0 {
			continue
		}
		scope := float64(scopePPM) / FullWeightPPM
		evidence.Trace.Sources = slices.Clone(trace.Sources)
		scopedUsage, err := a.total.add(trace, scope)
		if err != nil {
			return err
		}
		evidence.Trace.Usage = scopedUsage
		remaining := FullWeightPPM
		for _, binding := range bindings {
			if !overlapsWindow(binding.Meta.Effective, trace.Range) {
				continue
			}
			if !binding.Meta.covers(trace.Range) {
				return fmt.Errorf("%w: category binding changes inside a measured interval", ErrMappingBoundary)
			}
			if !proportional && scopePPM < FullWeightPPM && len(binding.Allocations) > 0 &&
				(len(binding.Allocations) != 1 || binding.Allocations[0].WeightPPM != FullWeightPPM) {
				return fmt.Errorf("%w: partial object and category shares require an explicit proportional policy", ErrInvalidClassification)
			}
			evidence.CategoryBindingVersion = binding.Meta.Version
			evidence.Allocations = slices.Clone(binding.Allocations)
			for _, allocation := range binding.Allocations {
				if tree.nodes[allocation.CategoryID].Energy != point.Result.Profile.Energy {
					return fmt.Errorf("%w: category %q uses a different energy", ErrInvalidClassification, allocation.CategoryID)
				}
				weight := scope * float64(allocation.WeightPPM) / FullWeightPPM
				id := allocation.CategoryID
				if a.nodes[id] == nil {
					a.nodes[id] = &CategoryValue{}
				}
				usage, err := a.nodes[id].add(trace, weight)
				if err != nil {
					return err
				}
				evidence.Contributions = append(evidence.Contributions, CategoryContribution{CategoryID: id, Usage: usage})
				remaining -= allocation.WeightPPM
			}
			break
		}
		if remaining > 0 {
			usage, err := a.unclassified.add(trace, scope*float64(remaining)/FullWeightPPM)
			if err != nil {
				return err
			}
			evidence.UnclassifiedUsage = usage
		}
		*intervals = append(*intervals, evidence)
	}
	return nil
}

func (value *CategoryValue) add(trace IntervalTrace, weight float64) (*float64, error) {
	value.Coverage.Expected += weight
	switch {
	case trace.Usage != nil:
		value.Coverage.Valid += weight
		usage := *trace.Usage * weight
		return &usage, value.addUsage(usage)
	case trace.Quality == Missing:
		value.Coverage.Missing += weight
	default:
		value.Coverage.Suspect += weight
	}
	return nil, nil
}

func (value *CategoryValue) addUsage(amount float64) error {
	if value.Usage == nil {
		value.Usage = new(float64)
	}
	*value.Usage += amount
	if !finite(*value.Usage) {
		return fmt.Errorf("%w: aggregate exceeds numeric range", ErrInvalidClassification)
	}
	return nil
}

func (a *categoryAccumulator) rows(tree categoryTree, energy EnergyType) ([]CategoryRow, error) {
	rows := make(map[string]CategoryRow)
	for i := len(tree.order) - 1; i >= 0; i-- {
		id := tree.order[i]
		category := tree.nodes[id]
		if category.Energy != energy {
			continue
		}
		row := CategoryRow{Category: category}
		if value := a.nodes[id]; value != nil {
			row.CategoryValue = *value
		}
		for _, child := range tree.children[id] {
			childRow := rows[child]
			coverage := childRow.Coverage
			row.Coverage.Expected += coverage.Expected
			row.Coverage.Valid += coverage.Valid
			row.Coverage.Missing += coverage.Missing
			row.Coverage.Suspect += coverage.Suspect
			if childRow.Usage != nil {
				if err := row.addUsage(*childRow.Usage); err != nil {
					return nil, err
				}
			}
			row.Children = append(row.Children, childRow)
		}
		rows[id] = row
	}
	var roots []CategoryRow
	for _, root := range tree.children[""] {
		if tree.nodes[root].Energy == energy {
			roots = append(roots, rows[root])
		}
	}
	return roots, nil
}
