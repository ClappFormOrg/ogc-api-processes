package plotbag

// Run is the process entry point: it validates the raw execution inputs the
// same way the notebook's PlotBagMatchProcessor.execute did and returns the
// process outputs keyed by output ID.
func Run(inputs map[string]any) (map[string]any, error) {
	plot, ok := inputs["plot"].(map[string]any)
	if !ok || plot["geometry"] == nil || plot["plan_dataid"] == nil {
		Logger.Warn("plot-bag-matching: input 'plot' missing or lacks 'plan_dataid'/'geometry'")
		return nil, inputErr("input 'plot' is required and must contain 'plan_dataid' and 'geometry'")
	}

	var bagBuildings []any
	if v, present := inputs["bag_buildings"]; present && v != nil {
		list, ok := v.([]any)
		if !ok {
			Logger.Warn("bag_buildings is not a list", "plan_dataid", plot["plan_dataid"])
			return nil, inputErr("bag_buildings must be a list")
		}
		bagBuildings = list
	}

	minOverlap, err := ratioInput(inputs, "min_overlap_ratio", DefaultMinOverlapRatio)
	if err != nil {
		return nil, err
	}
	fullThreshold, err := ratioInput(inputs, "full_match_threshold", DefaultFullMatchThreshold)
	if err != nil {
		return nil, err
	}

	Logger.Info("plot-bag-matching invoked", "plan_dataid", plot["plan_dataid"])
	result, err := MatchPlotToBAG(plot, bagBuildings, minOverlap, fullThreshold)
	if err != nil {
		return nil, err
	}
	return map[string]any{"match_result": result}, nil
}

func ratioInput(inputs map[string]any, name string, def float64) (float64, error) {
	v, present := inputs[name]
	if !present || v == nil {
		return def, nil
	}
	f, ok := v.(float64)
	if !ok {
		return 0, inputErr("%s must be a number", name)
	}
	if f < 0 || f > 1 {
		return 0, inputErr("%s must be between 0 and 1", name)
	}
	return f, nil
}
