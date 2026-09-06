package model

import (
	"errors"
	"slices"
	"strings"

	pluginsdk "github.com/sakullla/nginx-reverse-emby/plugin-sdk/go"
)

const DatasetArtifactKind = "dataset-index-v1"

// DatasetSnapshot is an immutable revision-bound data artifact, never an
// executable plugin package. LocalPath is filled only by verified Host/Agent
// materialization and is not accepted as an authority from remote snapshots.
type DatasetSnapshot struct {
	Version  pluginsdk.DatasetVersion `json:"version"`
	Artifact DatasetArtifact          `json:"artifact"`
	Bindings []DatasetInstanceBinding `json:"bindings"`
}

type DatasetArtifact struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	LocalPath string `json:"local_path,omitempty"`
}

type DatasetInstanceBinding struct {
	InstanceID      string                            `json:"instance_id"`
	Classifications []pluginsdk.DatasetClassification `json:"classifications"`
}

func (snapshot DatasetSnapshot) Validate() error {
	if err := snapshot.Version.Validate(); err != nil {
		return err
	}
	artifact := snapshot.Artifact
	if snapshot.Version.IndexDigest != "sha256:"+artifact.SHA256 || snapshot.Version.IndexBytes != artifact.SizeBytes {
		return errors.New("dataset artifact differs from index manifest")
	}
	if artifact.Kind != DatasetArtifactKind || artifact.ID != "dataset-"+artifact.SHA256 || len(artifact.SHA256) != 64 || strings.Trim(artifact.SHA256, "0123456789abcdef") != "" || artifact.SizeBytes <= 0 || artifact.SizeBytes > pluginsdk.DatasetDefaultIndexBudgetBytes {
		return errors.New("dataset artifact identity or size is invalid")
	}
	if len(snapshot.Bindings) == 0 || len(snapshot.Bindings) > 4096 {
		return errors.New("dataset bindings are empty or exceed the bound")
	}
	seen := make(map[string]bool)
	for _, binding := range snapshot.Bindings {
		if pluginsdk.ValidatePolicyIdentity(binding.InstanceID) != nil || seen[binding.InstanceID] || len(binding.Classifications) == 0 || len(binding.Classifications) > pluginsdk.DatasetMaxQueryClassifications {
			return errors.New("dataset instance binding is invalid")
		}
		seen[binding.InstanceID] = true
		for _, classification := range binding.Classifications {
			if err := classification.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func CloneDatasetSnapshots(values []DatasetSnapshot) []DatasetSnapshot {
	cloned := slices.Clone(values)
	for i, value := range values {
		cloned[i].Bindings = slices.Clone(value.Bindings)
		for j, binding := range value.Bindings {
			classes := slices.Clone(binding.Classifications)
			for k, classification := range classes {
				classes[k].Attributes = slices.Clone(classification.Attributes)
				for l, attribute := range classification.Attributes {
					if attribute.Boolean != nil {
						value := *attribute.Boolean
						classes[k].Attributes[l].Boolean = &value
					}
					if attribute.Integer != nil {
						value := *attribute.Integer
						classes[k].Attributes[l].Integer = &value
					}
				}
			}
			cloned[i].Bindings[j].Classifications = classes
		}
	}
	return cloned
}
