package oracle

import (
	"fmt"
	"oracle-diff-monitor/internal/models"
	"sort"
	"strings"
	"sync"
)

type ProgressFunc func(processed, total int, table, message string)
type TableResultFunc func(processed, total int, table string, diffs []models.DiffDetail) error

type Comparator struct {
	source *Client
	target *Client
}

func NewComparator(source, target *Client) *Comparator {
	return &Comparator{source: source, target: target}
}

func (c *Comparator) Compare(schema, tableFilter string, selectedTables []string) ([]models.DiffDetail, error) {
	return c.CompareWithResume(schema, tableFilter, nil, nil, nil, selectedTables)
}

func (c *Comparator) CompareWithProgress(schema, tableFilter string, progress ProgressFunc, selectedTables []string) ([]models.DiffDetail, error) {
	return c.CompareWithResume(schema, tableFilter, nil, progress, nil, selectedTables)
}

// CompareWithResume compares tables one-by-one with progress and resume support.
func (c *Comparator) CompareWithResume(schema, tableFilter string, completed map[string]bool, progress ProgressFunc, onTable TableResultFunc, selectedTables []string) ([]models.DiffDetail, error) {
	var diffs []models.DiffDetail

	sourceTables, err := c.source.GetTables(schema, tableFilter)
	if err != nil {
		return nil, fmt.Errorf("get source tables: %w", err)
	}
	targetTables, err := c.target.GetTables(schema, tableFilter)
	if err != nil {
		return nil, fmt.Errorf("get target tables: %w", err)
	}

	sourceMap := toSet(sourceTables)
	targetMap := toSet(targetTables)

	allTables := make(map[string]bool)
	for _, t := range sourceTables {
		allTables[t] = true
	}
	for _, t := range targetTables {
		allTables[t] = true
	}

	tableList := make([]string, 0, len(allTables))
	for t := range allTables {
		tableList = append(tableList, t)
	}
	sort.Strings(tableList)

	// If specific tables are selected, filter the list
	selectedSet := toSet(selectedTables)
	if len(selectedSet) > 0 {
		filtered := make([]string, 0, len(selectedTables))
		for _, t := range tableList {
			if selectedSet[t] {
				filtered = append(filtered, t)
			}
		}
		tableList = filtered
	}

	processed := 0
	for _, t := range tableList {
		if completed != nil && completed[t] {
			processed++
		}
	}

	for _, t := range tableList {
		if completed != nil && completed[t] {
			continue
		}
		if progress != nil {
			progress(processed, len(tableList), t, "正在比对表结构")
		}

		_, inSource := sourceMap[t]
		_, inTarget := targetMap[t]
		var tableDiffs []models.DiffDetail

		if !inSource {
			tableDiffs = append(tableDiffs, models.DiffDetail{
				TableName:   t,
				DiffType:    "missing_table",
				SourceValue: "不存在",
				TargetValue: "存在",
			})
		} else if !inTarget {
			tableDiffs = append(tableDiffs, models.DiffDetail{
				TableName:   t,
				DiffType:    "extra_table",
				SourceValue: "存在",
				TargetValue: "不存在",
			})
		} else {
			owner := schema
			if owner == "" {
				owner = c.source.config.Username
			}

			tableDiffs, err = c.compareTable(owner, t)
			if err != nil {
				return nil, fmt.Errorf("compare table %s: %w", t, err)
			}
		}

		processed++
		if onTable != nil {
			if err := onTable(processed, len(tableList), t, tableDiffs); err != nil {
				return nil, err
			}
		}
		diffs = append(diffs, tableDiffs...)
	}

	return diffs, nil
}

// CompareAllTables is an optimized batch version: fetches ALL metadata upfront
// (columns, indexes, constraints) for both source and target in a handful of queries,
// then compares in memory table-by-table.
func (c *Comparator) CompareAllTables(schema, tableFilter string, progress ProgressFunc, selectedTables []string) ([]models.DiffDetail, error) {
	var diffs []models.DiffDetail

	// 1) Get table lists (parallel)
	var (
		sourceTables []string
		targetTables []string
		wg           sync.WaitGroup
		err1, err2   error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		sourceTables, err1 = c.source.GetTables(schema, tableFilter)
	}()
	go func() {
		defer wg.Done()
		targetTables, err2 = c.target.GetTables(schema, tableFilter)
	}()
	wg.Wait()
	if err1 != nil {
		return nil, fmt.Errorf("get source tables: %w", err1)
	}
	if err2 != nil {
		return nil, fmt.Errorf("get target tables: %w", err2)
	}

	sourceSet := toSet(sourceTables)
	targetSet := toSet(targetTables)

	allTables := make(map[string]bool)
	for _, t := range sourceTables {
		allTables[t] = true
	}
	for _, t := range targetTables {
		allTables[t] = true
	}

	tableList := make([]string, 0, len(allTables))
	for t := range allTables {
		tableList = append(tableList, t)
	}
	sort.Strings(tableList)

	// If specific tables are selected, filter the list
	selectedSet := toSet(selectedTables)
	if len(selectedSet) > 0 {
		filtered := make([]string, 0, len(selectedTables))
		for _, t := range tableList {
			if selectedSet[t] {
				filtered = append(filtered, t)
			}
		}
		tableList = filtered
	}

	owner := schema
	if owner == "" {
		owner = c.source.config.Username
	}

	// 2) Pre-compute which tables need detailed comparison
	var compareTables []string
	for _, t := range tableList {
		if sourceSet[t] && targetSet[t] {
			compareTables = append(compareTables, t)
		}
	}
	// Further filter compareTables by selected set
	if len(selectedSet) > 0 {
		filtered := compareTables[:0]
		for _, t := range compareTables {
			if selectedSet[t] {
				filtered = append(filtered, t)
			}
		}
		compareTables = filtered
	}

	// 3) Batch fetch all metadata (parallel source/target)
	var (
		sourceColsMap map[string][]models.ColumnInfo
		targetColsMap map[string][]models.ColumnInfo
	)
	var (
		sourceIdxMap map[string][]models.IndexInfo
		targetIdxMap map[string][]models.IndexInfo
	)
	var (
		sourceConMap map[string][]models.ConstraintInfo
		targetConMap map[string][]models.ConstraintInfo
	)
	var (
		err3, err4 error
		err5, err6 error
		err7, err8 error
	)

	if len(compareTables) > 0 {
		wg.Add(6)
		go func() {
			defer wg.Done()
			sourceColsMap, err3 = c.source.GetAllColumns(owner, compareTables)
		}()
		go func() {
			defer wg.Done()
			targetColsMap, err4 = c.target.GetAllColumns(owner, compareTables)
		}()
		go func() {
			defer wg.Done()
			sourceIdxMap, err5 = c.source.GetAllIndexes(owner, compareTables)
		}()
		go func() {
			defer wg.Done()
			targetIdxMap, err6 = c.target.GetAllIndexes(owner, compareTables)
		}()
		go func() {
			defer wg.Done()
			sourceConMap, err7 = c.source.GetAllConstraints(owner, compareTables)
		}()
		go func() {
			defer wg.Done()
			targetConMap, err8 = c.target.GetAllConstraints(owner, compareTables)
		}()
		wg.Wait()
		if err3 != nil {
			return nil, fmt.Errorf("get source columns: %w", err3)
		}
		if err4 != nil {
			return nil, fmt.Errorf("get target columns: %w", err4)
		}
		if err5 != nil {
			return nil, fmt.Errorf("get source indexes: %w", err5)
		}
		if err6 != nil {
			return nil, fmt.Errorf("get target indexes: %w", err6)
		}
		if err7 != nil {
			return nil, fmt.Errorf("get source constraints: %w", err7)
		}
		if err8 != nil {
			return nil, fmt.Errorf("get target constraints: %w", err8)
		}
	}

	if progress != nil {
		progress(0, len(tableList), "", "正在比对差异")
	}

	// 4) Compare each table in-memory using a worker pool
	numWorkers := 8
	if len(tableList) < numWorkers {
		numWorkers = len(tableList)
	}
	if numWorkers < 1 {
		numWorkers = 1
	}

	type tableResult struct {
		table string
		diffs []models.DiffDetail
	}

	workCh := make(chan string, len(tableList))
	resultCh := make(chan tableResult, len(tableList))

	// Start workers
	var workerWg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for t := range workCh {
				if !sourceSet[t] {
					resultCh <- tableResult{t, []models.DiffDetail{{
						TableName: t, DiffType: "missing_table",
						SourceValue: "不存在", TargetValue: "存在",
					}}}
				} else if !targetSet[t] {
					resultCh <- tableResult{t, []models.DiffDetail{{
						TableName: t, DiffType: "extra_table",
						SourceValue: "存在", TargetValue: "不存在",
					}}}
				} else {
					td := compareTableFromMaps(t,
						sourceColsMap[t], targetColsMap[t],
						sourceIdxMap[t], targetIdxMap[t],
						sourceConMap[t], targetConMap[t],
					)
					resultCh <- tableResult{t, td}
				}
			}
		}()
	}

	// Send work
	for _, t := range tableList {
		workCh <- t
	}
	close(workCh)

	// Close resultCh when all workers finish
	go func() {
		workerWg.Wait()
		close(resultCh)
	}()

	// Collect results and report progress
	completed := 0
	resultMap := make(map[string][]models.DiffDetail, len(tableList))
	for r := range resultCh {
		resultMap[r.table] = r.diffs
		completed++
		if progress != nil {
			progress(completed, len(tableList), r.table, "正在比对差异")
		}
	}

	// Build final diffs in table order
	for _, t := range tableList {
		diffs = append(diffs, resultMap[t]...)
	}

	return diffs, nil
}

// compareTableFromMaps compares a single table using pre-fetched in-memory metadata.
func compareTableFromMaps(
	table string,
	sourceCols, targetCols []models.ColumnInfo,
	sourceIdxs, targetIdxs []models.IndexInfo,
	sourceCons, targetCons []models.ConstraintInfo,
) []models.DiffDetail {
	var diffs []models.DiffDetail

	// Compare columns
	diffs = append(diffs, compareColsFromMaps(table, sourceCols, targetCols)...)

	// Compare indexes
	diffs = append(diffs, compareIndexesFromMaps(table, sourceIdxs, targetIdxs)...)

	// Compare constraints
	sourcePK := filterConstraints(sourceCons, "P")
	targetPK := filterConstraints(targetCons, "P")
	diffs = append(diffs, comparePK(table, sourcePK, targetPK)...)

	sourceFK := filterConstraints(sourceCons, "R")
	targetFK := filterConstraints(targetCons, "R")
	diffs = append(diffs, compareFK(table, sourceFK, targetFK)...)

	sourceOther := filterConstraints(sourceCons, "C", "U")
	targetOther := filterConstraints(targetCons, "C", "U")
	diffs = append(diffs, compareOtherCons(table, sourceOther, targetOther)...)

	return diffs
}

func compareColsFromMaps(table string, sourceCols, targetCols []models.ColumnInfo) []models.DiffDetail {
	var diffs []models.DiffDetail

	sourceMap := make(map[string]models.ColumnInfo)
	for _, col := range sourceCols {
		sourceMap[col.ColumnName] = col
	}
	targetMap := make(map[string]models.ColumnInfo)
	for _, col := range targetCols {
		targetMap[col.ColumnName] = col
	}

	allCols := make(map[string]bool)
	for _, col := range sourceCols {
		allCols[col.ColumnName] = true
	}
	for _, col := range targetCols {
		allCols[col.ColumnName] = true
	}

	for colName := range allCols {
		sCol, inSource := sourceMap[colName]
		tCol, inTarget := targetMap[colName]

		if !inSource {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "missing_column",
				ColumnName:  colName,
				SourceValue: "不存在",
				TargetValue: tCol.DataType,
			})
			continue
		}
		if !inTarget {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "extra_column",
				ColumnName:  colName,
				SourceValue: sCol.DataType,
				TargetValue: "不存在",
			})
			continue
		}

		if normaliseType(sCol.DataType) != normaliseType(tCol.DataType) {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "type_mismatch",
				ColumnName:  colName,
				SourceValue: sCol.DataType,
				TargetValue: tCol.DataType,
			})
		} else {
			if sCol.DataLength != tCol.DataLength {
				diffs = append(diffs, models.DiffDetail{
					TableName:   table,
					DiffType:    "length_mismatch",
					ColumnName:  colName,
					SourceValue: fmt.Sprintf("%d", sCol.DataLength),
					TargetValue: fmt.Sprintf("%d", tCol.DataLength),
				})
			}
			if sCol.DataPrecision != tCol.DataPrecision {
				diffs = append(diffs, models.DiffDetail{
					TableName:   table,
					DiffType:    "precision_mismatch",
					ColumnName:  colName,
					SourceValue: fmt.Sprintf("%d", sCol.DataPrecision),
					TargetValue: fmt.Sprintf("%d", tCol.DataPrecision),
				})
			}
			if sCol.DataScale != tCol.DataScale {
				diffs = append(diffs, models.DiffDetail{
					TableName:   table,
					DiffType:    "scale_mismatch",
					ColumnName:  colName,
					SourceValue: fmt.Sprintf("%d", sCol.DataScale),
					TargetValue: fmt.Sprintf("%d", tCol.DataScale),
				})
			}
		}

		if sCol.Nullable != tCol.Nullable {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "nullable_mismatch",
				ColumnName:  colName,
				SourceValue: sCol.Nullable,
				TargetValue: tCol.Nullable,
			})
		}

		if sCol.DataDefault != tCol.DataDefault {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "default_mismatch",
				ColumnName:  colName,
				SourceValue: sCol.DataDefault,
				TargetValue: tCol.DataDefault,
			})
		}

		if sCol.ColumnID != tCol.ColumnID {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "column_order_mismatch",
				ColumnName:  colName,
				SourceValue: fmt.Sprintf("顺序 %d", sCol.ColumnID),
				TargetValue: fmt.Sprintf("顺序 %d", tCol.ColumnID),
			})
		}
	}
	return diffs
}

func compareIndexesFromMaps(table string, sourceIdxs, targetIdxs []models.IndexInfo) []models.DiffDetail {
	var diffs []models.DiffDetail

	sourceMap := make(map[string]models.IndexInfo)
	for _, idx := range sourceIdxs {
		sourceMap[idx.IndexName] = idx
	}
	targetMap := make(map[string]models.IndexInfo)
	for _, idx := range targetIdxs {
		targetMap[idx.IndexName] = idx
	}

	// Phase 1: name-based matching
	var extraByName []models.IndexInfo   // in source, not in target (by name)
	var missingByName []models.IndexInfo // in target, not in source (by name)

	allIdxs := make(map[string]bool)
	for _, idx := range sourceIdxs {
		allIdxs[idx.IndexName] = true
	}
	for _, idx := range targetIdxs {
		allIdxs[idx.IndexName] = true
	}

	for idxName := range allIdxs {
		sIdx, inSource := sourceMap[idxName]
		tIdx, inTarget := targetMap[idxName]

		if !inSource {
			missingByName = append(missingByName, tIdx)
			continue
		}
		if !inTarget {
			extraByName = append(extraByName, sIdx)
			continue
		}

		// Name matched — compare content
		if sIdx.IndexType != tIdx.IndexType || sIdx.Uniqueness != tIdx.Uniqueness {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "index_type_mismatch",
				ColumnName:  idxName,
				SourceValue: formatIndexInfo(sIdx),
				TargetValue: formatIndexInfo(tIdx),
			})
		} else if strings.Join(sIdx.Columns, ",") != strings.Join(tIdx.Columns, ",") {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "index_column_mismatch",
				ColumnName:  idxName,
				SourceValue: strings.Join(sIdx.Columns, ","),
				TargetValue: strings.Join(tIdx.Columns, ","),
			})
		}
	}

	// Phase 2: content-based matching for remaining unmatched indexes
	usedTarget := make(map[int]bool)
	for _, sIdx := range extraByName {
		matched := false
		for ti, tIdx := range missingByName {
			if usedTarget[ti] {
				continue
			}
			if indexContentKey(sIdx) == indexContentKey(tIdx) {
				usedTarget[ti] = true
				matched = true
				break
			}
		}
		if !matched {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "extra_index",
				ColumnName:  sIdx.IndexName,
				SourceValue: formatIndexInfo(sIdx),
				TargetValue: "不存在",
			})
		}
	}
	for ti, tIdx := range missingByName {
		if !usedTarget[ti] {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "missing_index",
				ColumnName:  tIdx.IndexName,
				SourceValue: "不存在",
				TargetValue: formatIndexInfo(tIdx),
			})
		}
	}

	return diffs
}

// ---- Original per-table comparison methods (kept for backward compatibility) ----

func (c *Comparator) compareTable(schema, table string) ([]models.DiffDetail, error) {
	var diffs []models.DiffDetail

	colDiffs, err := c.compareColumns(schema, table)
	if err != nil {
		return nil, err
	}
	diffs = append(diffs, colDiffs...)

	idxDiffs, err := c.compareIndexes(schema, table)
	if err != nil {
		return nil, err
	}
	diffs = append(diffs, idxDiffs...)

	conDiffs, err := c.compareConstraints(schema, table)
	if err != nil {
		return nil, err
	}
	diffs = append(diffs, conDiffs...)

	return diffs, nil
}

func fetchColsParallel(source, target *Client, schema, table string) ([]models.ColumnInfo, []models.ColumnInfo, error) {
	var (
		sourceCols []models.ColumnInfo
		targetCols []models.ColumnInfo
		sourceErr  error
		targetErr  error
		wg         sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		sourceCols, sourceErr = source.GetColumns(schema, table)
	}()
	go func() {
		defer wg.Done()
		targetCols, targetErr = target.GetColumns(schema, table)
	}()
	wg.Wait()
	if sourceErr != nil {
		return nil, nil, sourceErr
	}
	if targetErr != nil {
		return nil, nil, targetErr
	}
	return sourceCols, targetCols, nil
}

func (c *Comparator) compareColumns(schema, table string) ([]models.DiffDetail, error) {
	var diffs []models.DiffDetail

	sourceCols, targetCols, err := fetchColsParallel(c.source, c.target, schema, table)
	if err != nil {
		return nil, err
	}

	sourceMap := make(map[string]models.ColumnInfo)
	for _, col := range sourceCols {
		sourceMap[col.ColumnName] = col
	}
	targetMap := make(map[string]models.ColumnInfo)
	for _, col := range targetCols {
		targetMap[col.ColumnName] = col
	}

	allCols := make(map[string]bool)
	for _, col := range sourceCols {
		allCols[col.ColumnName] = true
	}
	for _, col := range targetCols {
		allCols[col.ColumnName] = true
	}

	for colName := range allCols {
		sCol, inSource := sourceMap[colName]
		tCol, inTarget := targetMap[colName]

		if !inSource {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "missing_column",
				ColumnName:  colName,
				SourceValue: "不存在",
				TargetValue: tCol.DataType,
			})
			continue
		}
		if !inTarget {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "extra_column",
				ColumnName:  colName,
				SourceValue: sCol.DataType,
				TargetValue: "不存在",
			})
			continue
		}

		if normaliseType(sCol.DataType) != normaliseType(tCol.DataType) {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "type_mismatch",
				ColumnName:  colName,
				SourceValue: sCol.DataType,
				TargetValue: tCol.DataType,
			})
		} else {
			if sCol.DataLength != tCol.DataLength {
				diffs = append(diffs, models.DiffDetail{
					TableName:   table,
					DiffType:    "length_mismatch",
					ColumnName:  colName,
					SourceValue: fmt.Sprintf("%d", sCol.DataLength),
					TargetValue: fmt.Sprintf("%d", tCol.DataLength),
				})
			}
			if sCol.DataPrecision != tCol.DataPrecision {
				diffs = append(diffs, models.DiffDetail{
					TableName:   table,
					DiffType:    "precision_mismatch",
					ColumnName:  colName,
					SourceValue: fmt.Sprintf("%d", sCol.DataPrecision),
					TargetValue: fmt.Sprintf("%d", tCol.DataPrecision),
				})
			}
			if sCol.DataScale != tCol.DataScale {
				diffs = append(diffs, models.DiffDetail{
					TableName:   table,
					DiffType:    "scale_mismatch",
					ColumnName:  colName,
					SourceValue: fmt.Sprintf("%d", sCol.DataScale),
					TargetValue: fmt.Sprintf("%d", tCol.DataScale),
				})
			}
		}

		if sCol.Nullable != tCol.Nullable {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "nullable_mismatch",
				ColumnName:  colName,
				SourceValue: sCol.Nullable,
				TargetValue: tCol.Nullable,
			})
		}

		if sCol.DataDefault != tCol.DataDefault {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "default_mismatch",
				ColumnName:  colName,
				SourceValue: sCol.DataDefault,
				TargetValue: tCol.DataDefault,
			})
		}

		if sCol.ColumnID != tCol.ColumnID {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "column_order_mismatch",
				ColumnName:  colName,
				SourceValue: fmt.Sprintf("顺序 %d", sCol.ColumnID),
				TargetValue: fmt.Sprintf("顺序 %d", tCol.ColumnID),
			})
		}
	}

	return diffs, nil
}

func fetchIndexesParallel(source, target *Client, schema, table string) ([]models.IndexInfo, []models.IndexInfo, error) {
	var (
		sourceIdxs []models.IndexInfo
		targetIdxs []models.IndexInfo
		sourceErr  error
		targetErr  error
		wg         sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		sourceIdxs, sourceErr = source.GetIndexes(schema, table)
	}()
	go func() {
		defer wg.Done()
		targetIdxs, targetErr = target.GetIndexes(schema, table)
	}()
	wg.Wait()
	if sourceErr != nil {
		return nil, nil, sourceErr
	}
	if targetErr != nil {
		return nil, nil, targetErr
	}
	return sourceIdxs, targetIdxs, nil
}

func (c *Comparator) compareIndexes(schema, table string) ([]models.DiffDetail, error) {
	sourceIdxs, targetIdxs, err := fetchIndexesParallel(c.source, c.target, schema, table)
	if err != nil {
		return nil, err
	}
	return compareIndexesFromMaps(table, sourceIdxs, targetIdxs), nil
}

func fetchConstraintsParallel(source, target *Client, schema, table string) ([]models.ConstraintInfo, []models.ConstraintInfo, error) {
	var (
		sourceCons []models.ConstraintInfo
		targetCons []models.ConstraintInfo
		sourceErr  error
		targetErr  error
		wg         sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		sourceCons, sourceErr = source.GetConstraints(schema, table)
	}()
	go func() {
		defer wg.Done()
		targetCons, targetErr = target.GetConstraints(schema, table)
	}()
	wg.Wait()
	if sourceErr != nil {
		return nil, nil, sourceErr
	}
	if targetErr != nil {
		return nil, nil, targetErr
	}
	return sourceCons, targetCons, nil
}

func (c *Comparator) compareConstraints(schema, table string) ([]models.DiffDetail, error) {
	var diffs []models.DiffDetail

	sourceCons, targetCons, err := fetchConstraintsParallel(c.source, c.target, schema, table)
	if err != nil {
		return nil, err
	}

	sourcePK := filterConstraints(sourceCons, "P")
	targetPK := filterConstraints(targetCons, "P")
	diffs = append(diffs, comparePK(table, sourcePK, targetPK)...)

	sourceFK := filterConstraints(sourceCons, "R")
	targetFK := filterConstraints(targetCons, "R")
	diffs = append(diffs, compareFK(table, sourceFK, targetFK)...)

	sourceOther := filterConstraints(sourceCons, "C", "U")
	targetOther := filterConstraints(targetCons, "C", "U")
	diffs = append(diffs, compareOtherCons(table, sourceOther, targetOther)...)

	return diffs, nil
}

// ---- Pure in-memory comparison functions (shared by old and new paths) ----

func comparePK(table string, source, target []models.ConstraintInfo) []models.DiffDetail {
	var diffs []models.DiffDetail

	sourceMap := make(map[string]models.ConstraintInfo)
	for _, c := range source {
		sourceMap[c.ConstraintName] = c
	}
	targetMap := make(map[string]models.ConstraintInfo)
	for _, c := range target {
		targetMap[c.ConstraintName] = c
	}

	// Phase 1: name-based matching
	var extraByName []models.ConstraintInfo
	var missingByName []models.ConstraintInfo

	all := make(map[string]bool)
	for _, c := range source {
		all[c.ConstraintName] = true
	}
	for _, c := range target {
		all[c.ConstraintName] = true
	}

	for name := range all {
		s, inSource := sourceMap[name]
		t, inTarget := targetMap[name]

		if !inSource {
			missingByName = append(missingByName, t)
			continue
		}
		if !inTarget {
			extraByName = append(extraByName, s)
			continue
		}
		if strings.Join(s.Columns, ",") != strings.Join(t.Columns, ",") {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "pk_column_mismatch",
				ColumnName:  name,
				SourceValue: strings.Join(s.Columns, ","),
				TargetValue: strings.Join(t.Columns, ","),
			})
		}
	}

	// Phase 2: content-based matching (same PK columns → same PK regardless of name)
	usedTarget := make(map[int]bool)
	for _, s := range extraByName {
		matched := false
		for ti, t := range missingByName {
			if usedTarget[ti] {
				continue
			}
			if pkContentKey(s) == pkContentKey(t) {
				usedTarget[ti] = true
				matched = true
				break
			}
		}
		if !matched {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "extra_pk",
				ColumnName:  s.ConstraintName,
				SourceValue: strings.Join(s.Columns, ","),
				TargetValue: "不存在",
			})
		}
	}
	for ti, t := range missingByName {
		if !usedTarget[ti] {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "missing_pk",
				ColumnName:  t.ConstraintName,
				SourceValue: "不存在",
				TargetValue: strings.Join(t.Columns, ","),
			})
		}
	}

	return diffs
}

func compareFK(table string, source, target []models.ConstraintInfo) []models.DiffDetail {
	var diffs []models.DiffDetail

	sourceMap := make(map[string]models.ConstraintInfo)
	for _, c := range source {
		sourceMap[c.ConstraintName] = c
	}
	targetMap := make(map[string]models.ConstraintInfo)
	for _, c := range target {
		targetMap[c.ConstraintName] = c
	}

	all := make(map[string]bool)
	for _, c := range source {
		all[c.ConstraintName] = true
	}
	for _, c := range target {
		all[c.ConstraintName] = true
	}

	for name := range all {
		s, inSource := sourceMap[name]
		t, inTarget := targetMap[name]

		if !inSource {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "missing_fk",
				ColumnName:  name,
				SourceValue: "不存在",
				TargetValue: formatFKInfo(t),
			})
			continue
		}
		if !inTarget {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "extra_fk",
				ColumnName:  name,
				SourceValue: formatFKInfo(s),
				TargetValue: "不存在",
			})
			continue
		}
		if strings.Join(s.Columns, ",") != strings.Join(t.Columns, ",") {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "fk_column_mismatch",
				ColumnName:  name,
				SourceValue: strings.Join(s.Columns, ","),
				TargetValue: strings.Join(t.Columns, ","),
			})
		} else if s.RTableName != t.RTableName || strings.Join(s.RColumns, ",") != strings.Join(t.RColumns, ",") {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "fk_ref_mismatch",
				ColumnName:  name,
				SourceValue: formatFKInfo(s),
				TargetValue: formatFKInfo(t),
			})
		}
	}
	return diffs
}

func compareOtherCons(table string, source, target []models.ConstraintInfo) []models.DiffDetail {
	var diffs []models.DiffDetail

	sourceMap := make(map[string]models.ConstraintInfo)
	for _, c := range source {
		sourceMap[c.ConstraintName] = c
	}
	targetMap := make(map[string]models.ConstraintInfo)
	for _, c := range target {
		targetMap[c.ConstraintName] = c
	}

	// Phase 1: name-based matching
	var extraByName []models.ConstraintInfo
	var missingByName []models.ConstraintInfo

	all := make(map[string]bool)
	for _, c := range source {
		all[c.ConstraintName] = true
	}
	for _, c := range target {
		all[c.ConstraintName] = true
	}

	for name := range all {
		s, inSource := sourceMap[name]
		t, inTarget := targetMap[name]

		if !inSource {
			missingByName = append(missingByName, t)
			continue
		}
		if !inTarget {
			extraByName = append(extraByName, s)
			continue
		}
		if s.SearchCondition != t.SearchCondition {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "constraint_condition_mismatch",
				ColumnName:  name,
				SourceValue: s.SearchCondition,
				TargetValue: t.SearchCondition,
			})
		}
	}

	// Phase 2: content-based matching (same condition/columns → same constraint)
	usedTarget := make(map[int]bool)
	for _, s := range extraByName {
		matched := false
		for ti, t := range missingByName {
			if usedTarget[ti] {
				continue
			}
			if otherConsContentKey(s) == otherConsContentKey(t) {
				usedTarget[ti] = true
				matched = true
				break
			}
		}
		if !matched {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "extra_constraint",
				ColumnName:  s.ConstraintName,
				SourceValue: formatOtherCons(s),
				TargetValue: "不存在",
			})
		}
	}
	for ti, t := range missingByName {
		if !usedTarget[ti] {
			diffs = append(diffs, models.DiffDetail{
				TableName:   table,
				DiffType:    "missing_constraint",
				ColumnName:  t.ConstraintName,
				SourceValue: "不存在",
				TargetValue: formatOtherCons(t),
			})
		}
	}

	return diffs
}

func normaliseType(t string) string {
	t = strings.ToUpper(strings.TrimSpace(t))
	if strings.HasPrefix(t, "TIMESTAMP") {
		return "TIMESTAMP"
	}
	if strings.HasPrefix(t, "INTERVAL") {
		return "INTERVAL"
	}
	return t
}

// indexContentKey returns a content fingerprint for an index, used for
// name-independent matching (handles auto-generated index names like SYS_Cnnnnn).
func indexContentKey(idx models.IndexInfo) string {
	return fmt.Sprintf("%s|%s|%s", idx.IndexType, idx.Uniqueness, strings.Join(idx.Columns, ","))
}

// pkContentKey returns a content fingerprint for a PRIMARY KEY constraint.
func pkContentKey(con models.ConstraintInfo) string {
	return strings.Join(con.Columns, ",")
}

// otherConsContentKey returns a content fingerprint for a CHECK or UNIQUE constraint.
func otherConsContentKey(con models.ConstraintInfo) string {
	if con.ConstraintType == "C" {
		return con.SearchCondition
	}
	// UNIQUE
	return strings.Join(con.Columns, ",")
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool)
	for _, item := range items {
		m[item] = true
	}
	return m
}

func filterConstraints(cons []models.ConstraintInfo, types ...string) []models.ConstraintInfo {
	var result []models.ConstraintInfo
	typeSet := make(map[string]bool)
	for _, t := range types {
		typeSet[t] = true
	}
	for _, c := range cons {
		if typeSet[c.ConstraintType] {
			result = append(result, c)
		}
	}
	return result
}

func formatIndexInfo(idx models.IndexInfo) string {
	unq := "NONUNIQUE"
	if idx.Uniqueness == "UNIQUE" {
		unq = "UNIQUE"
	}
	return fmt.Sprintf("%s %s (%s)", unq, idx.IndexType, strings.Join(idx.Columns, ","))
}

func formatFKInfo(con models.ConstraintInfo) string {
	return fmt.Sprintf(
		"%s(%s) -> %s(%s)",
		con.ConstraintName,
		strings.Join(con.Columns, ","),
		con.RTableName,
		strings.Join(con.RColumns, ","),
	)
}

func formatOtherCons(con models.ConstraintInfo) string {
	label := "CHECK"
	if con.ConstraintType == "U" {
		label = "UNIQUE"
	}
	if con.SearchCondition != "" {
		return fmt.Sprintf("%s (%s) [%s]", label, con.SearchCondition, con.Status)
	}
	if len(con.Columns) > 0 {
		return fmt.Sprintf("%s (%s) [%s]", label, strings.Join(con.Columns, ","), con.Status)
	}
	return fmt.Sprintf("%s [%s]", label, con.Status)
}
