package service

type BackupImportResult struct {
	Manifest BackupManifest      `json:"manifest"`
	Summary  BackupImportSummary `json:"summary"`
	Report   BackupImportReport  `json:"report"`
}

type BackupImportSummary struct {
	Imported               BackupCounts `json:"imported"`
	SkippedConflict        BackupCounts `json:"skipped_conflict"`
	SkippedInvalid         BackupCounts `json:"skipped_invalid"`
	SkippedMissingMaterial BackupCounts `json:"skipped_missing_material"`
}

type BackupImportReport struct {
	Imported               []BackupImportItem `json:"imported"`
	SkippedConflict        []BackupImportItem `json:"skipped_conflict"`
	SkippedInvalid         []BackupImportItem `json:"skipped_invalid"`
	SkippedMissingMaterial []BackupImportItem `json:"skipped_missing_material"`
}

type BackupImportItem struct {
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Reason string `json:"reason,omitempty"`
}

func newBackupImportResult(manifest BackupManifest) BackupImportResult {
	return BackupImportResult{
		Manifest: manifest,
		Summary:  BackupImportSummary{},
		Report: BackupImportReport{
			Imported:               []BackupImportItem{},
			SkippedConflict:        []BackupImportItem{},
			SkippedInvalid:         []BackupImportItem{},
			SkippedMissingMaterial: []BackupImportItem{},
		},
	}
}

func (r *BackupImportResult) addImported(kind string, key string) {
	r.Report.Imported = append(r.Report.Imported, BackupImportItem{Kind: kind, Key: key})
	r.Summary.Imported.increment(kind)
}

func (r *BackupImportResult) addSkippedConflict(kind string, key string, reason string) {
	r.Report.SkippedConflict = append(r.Report.SkippedConflict, BackupImportItem{Kind: kind, Key: key, Reason: reason})
	r.Summary.SkippedConflict.increment(kind)
}

func (r *BackupImportResult) addSkippedInvalid(kind string, key string, reason string) {
	r.Report.SkippedInvalid = append(r.Report.SkippedInvalid, BackupImportItem{Kind: kind, Key: key, Reason: reason})
	r.Summary.SkippedInvalid.increment(kind)
}

func (r *BackupImportResult) addSkippedMissingMaterial(kind string, key string, reason string) {
	r.Report.SkippedMissingMaterial = append(r.Report.SkippedMissingMaterial, BackupImportItem{Kind: kind, Key: key, Reason: reason})
	r.Summary.SkippedMissingMaterial.increment(kind)
}

func (c *BackupCounts) increment(kind string) {
	switch kind {
	case "agent":
		c.Agents++
	case "http_rule":
		c.HTTPRules++
	case "l4_rule":
		c.L4Rules++
	case "relay_listener":
		c.RelayListeners++
	case "certificate":
		c.Certificates++
	case "version_policy":
		c.VersionPolicies++
	}
}
