package service

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type RecoveryDatabaseTarget struct {
	Entry     string `json:"entry"`
	Container string `json:"container"`
	Database  string `json:"database"`
	User      string `json:"user"`
}
type RecoveryRestoreRequest struct {
	Passphrase     string                   `json:"passphrase"`
	Volumes        map[string]string        `json:"volumes"`
	InstanceVolume string                   `json:"instance_volume"`
	Databases      []RecoveryDatabaseTarget `json:"databases"`
	Projects       map[string]string        `json:"projects"`
}
type RecoveryRestorePlan struct {
	InstanceCompose string           `json:"instance_compose,omitempty"`
	Manifest        RecoveryManifest `json:"manifest"`
	Ready           bool             `json:"ready"`
	Checks          []PlanCheck      `json:"checks"`
	Instructions    []string         `json:"instructions"`
}

func (s *RecoveryService) unpack(id, pass string) (RecoveryManifest, string, error) {
	if err := os.MkdirAll(filepath.Join(s.Directory, "tmp"), 0700); err != nil {
		return RecoveryManifest{}, "", err
	}
	directory, err := os.MkdirTemp(filepath.Join(s.Directory, "tmp"), "restore-")
	if err != nil {
		return RecoveryManifest{}, "", err
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(directory)
		}
	}()
	encrypted, err := s.Download(id)
	if err != nil {
		return RecoveryManifest{}, "", err
	}
	defer encrypted.Close()
	plainPath := filepath.Join(directory, "plain.tar")
	plain, err := os.OpenFile(plainPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return RecoveryManifest{}, "", err
	}
	defer plain.Close()
	if err = DecryptRecovery(plain, encrypted, pass, recoveryMaximum+(2<<20)); err != nil {
		return RecoveryManifest{}, "", err
	}
	if _, err = plain.Seek(0, 0); err != nil {
		return RecoveryManifest{}, "", err
	}
	tr := tar.NewReader(plain)
	header, err := tr.Next()
	if err != nil || header.Name != "manifest.json" || header.Typeflag != tar.TypeReg || header.Size > 1<<20 {
		return RecoveryManifest{}, "", fmt.Errorf("invalid recovery manifest")
	}
	raw, err := io.ReadAll(tr)
	if err != nil {
		return RecoveryManifest{}, "", err
	}
	var manifest RecoveryManifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Version != 1 || len(manifest.Entries) == 0 || len(manifest.Entries) > 221 {
		return manifest, "", fmt.Errorf("unsupported recovery manifest")
	}
	expected := map[string]RecoveryEntry{}
	for index, entry := range manifest.Entries {
		file := fmt.Sprintf("objects/%03d", index)
		if entry.File != file || entry.Size < 0 || entry.Size > recoveryMaximum || len(entry.SHA256) != 64 {
			return manifest, "", fmt.Errorf("invalid recovery object metadata")
		}
		switch entry.Kind {
		case "volume", "instance", "project", "database":
		default:
			return manifest, "", fmt.Errorf("unknown recovery object kind")
		}
		if entry.Kind == "database" && (entry.Engine != "postgres" && entry.Engine != "mysql" && entry.Engine != "mariadb" || entry.Major == "") {
			return manifest, "", fmt.Errorf("unverified database engine/version")
		}
		expected[file] = entry
	}
	var total int64
	for {
		header, err = tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return manifest, "", err
		}
		entry, exists := expected[header.Name]
		if !exists || header.Typeflag != tar.TypeReg || header.Size != entry.Size {
			return manifest, "", fmt.Errorf("unexpected recovery object")
		}
		total += header.Size
		if total > recoveryMaximum {
			return manifest, "", fmt.Errorf("recovery archive exceeds size limit")
		}
		file, err := os.OpenFile(filepath.Join(directory, strings.ReplaceAll(entry.File, "/", "-")), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return manifest, "", err
		}
		hash := sha256.New()
		_, copyErr := io.CopyN(io.MultiWriter(file, hash), tr, header.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return manifest, "", copyErr
		}
		if closeErr != nil {
			return manifest, "", closeErr
		}
		if hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return manifest, "", fmt.Errorf("recovery object checksum mismatch")
		}
		delete(expected, header.Name)
	}
	if len(expected) != 0 {
		return manifest, "", fmt.Errorf("recovery archive is missing objects")
	}
	_, err = s.DB.Exec(`UPDATE recovery_backups SET integrity_verified_at=? WHERE id=?`, time.Now().Unix(), id)
	if err != nil {
		return manifest, "", err
	}
	keep = true
	return manifest, directory, nil
}
func (s *RecoveryService) PlanRestore(ctx context.Context, id string, request RecoveryRestoreRequest) (RecoveryRestorePlan, error) {
	manifest, directory, err := s.unpack(id, request.Passphrase)
	if err != nil {
		return RecoveryRestorePlan{}, err
	}
	defer os.RemoveAll(directory)
	return s.restorePlan(ctx, manifest, request), nil
}
func (s *RecoveryService) restorePlan(ctx context.Context, manifest RecoveryManifest, request RecoveryRestoreRequest) RecoveryRestorePlan {
	plan := RecoveryRestorePlan{Manifest: manifest, Ready: true, Checks: []PlanCheck{}, Instructions: []string{"No existing volume or project folder will be overwritten.", "Instance restoration creates a new data volume; switch the core to it only after reviewing paths, provider identities and credentials.", "A verified archive checksum does not establish successful application/database recovery."}}
	validVolumes := map[string]bool{}
	validProjects := map[string]bool{}
	validDatabases := map[string]bool{}
	hasInstance := false
	for _, entry := range manifest.Entries {
		switch entry.Kind {
		case "volume":
			validVolumes[entry.Name] = true
		case "project":
			validProjects[entry.Name] = true
		case "database":
			validDatabases[entry.File] = true
		case "instance":
			hasInstance = true
		}
	}
	invalid := false
	for key := range request.Volumes {
		if !validVolumes[key] {
			invalid = true
		}
	}
	for key := range request.Projects {
		if !validProjects[key] {
			invalid = true
		}
	}
	seenEntries := map[string]bool{}
	seenTargets := map[string]bool{}
	for _, target := range request.Databases {
		key := target.Container + "/" + target.Database
		if !validDatabases[target.Entry] || seenEntries[target.Entry] || seenTargets[key] {
			invalid = true
		}
		seenEntries[target.Entry] = true
		seenTargets[key] = true
	}
	if request.InstanceVolume != "" && !hasInstance {
		invalid = true
	}
	if invalid {
		plan.Ready = false
		plan.Checks = append(plan.Checks, PlanCheck{"mappings", "Recovery mappings", "failed", "Unknown or duplicate recovery mappings"})
		return plan
	}
	selected := 0
	destinations := map[string]bool{}
	checkVolume := func(key, name string) {
		if !recoveryVolumeIdentifier.MatchString(name) {
			plan.Ready = false
			plan.Checks = append(plan.Checks, PlanCheck{key, "New volume", "failed", "Choose a new safe volume name"})
			return
		}
		if destinations[name] {
			plan.Ready = false
			plan.Checks = append(plan.Checks, PlanCheck{key, "New volume", "failed", "Each recovery object needs its own destination"})
			return
		}
		destinations[name] = true
		if _, err := inspectRecoveryVolume(ctx, name); err == nil {
			plan.Ready = false
			plan.Checks = append(plan.Checks, PlanCheck{key, "New volume", "failed", "Destination already exists; existing volume data is never overwritten"})
		} else if !recoveryVolumeMissing(err) {
			plan.Ready = false
			plan.Checks = append(plan.Checks, PlanCheck{key, "New volume", "failed", "Docker volume availability could not be verified"})
		} else {
			plan.Checks = append(plan.Checks, PlanCheck{key, "New volume", "passed", name + " will be created"})
		}
	}
	for _, entry := range manifest.Entries {
		switch entry.Kind {
		case "volume":
			if name := request.Volumes[entry.Name]; name != "" {
				selected++
				checkVolume(entry.File, name)
			}
		case "instance":
			if request.InstanceVolume != "" {
				selected++
				checkVolume(entry.File, request.InstanceVolume)
				compose := map[string]any{"name": "docktun-recovered", "services": map[string]any{"docktun": map[string]any{"image": entry.Image, "restart": "unless-stopped", "ports": []string{"127.0.0.1:8080:3001"}, "environment": map[string]string{"DB_PATH": "/app/data/app.db", "DOCKTUN_RECOVERY_MODE": "true", "DOCKTUN_PREPARE_PROVIDERS": "false", "CLOUDFLARED_CONFIG_PATH": "/app/data/cloudflared/config.yml", "MESH_CONFIG_PATH": "/app/data/mesh.json"}, "volumes": []string{"recovered-data:/app/data", "/var/run/docker.sock:/var/run/docker.sock"}}}, "volumes": map[string]any{"recovered-data": map[string]any{"external": true, "name": request.InstanceVolume}}}
				if raw, e := yaml.Marshal(compose); e == nil {
					plan.InstanceCompose = string(raw)
				}
				plan.Instructions = append(plan.Instructions, "Save the recovery Compose preview separately and run it only after stopping the original core. It binds to loopback for review. Add the intended projects bind mount, repair project paths and review provider credentials. Remove DOCKTUN_RECOVERY_MODE and DOCKTUN_PREPARE_PROVIDERS overrides only after confirming recovery review.")

			}
		case "project":
			if destination := request.Projects[entry.Name]; destination != "" {
				selected++
				if !filepath.IsAbs(destination) {
					plan.Ready = false
					plan.Checks = append(plan.Checks, PlanCheck{entry.File, "Project destination", "failed", "An unused absolute project path is required"})
				} else if _, err := os.Lstat(destination); !os.IsNotExist(err) {
					plan.Ready = false
					plan.Checks = append(plan.Checks, PlanCheck{entry.File, "Project destination", "failed", "Project destination already exists"})
				} else {
					plan.Checks = append(plan.Checks, PlanCheck{entry.File, "Project destination", "passed", destination})
				}
			}
		case "database":
			for _, target := range request.Databases {
				if target.Entry != entry.File {
					continue
				}
				selected++
				if err := validateDatabaseRestoreTarget(ctx, entry, target); err != nil {
					plan.Ready = false
					plan.Checks = append(plan.Checks, PlanCheck{entry.File, "Empty database target", "failed", err.Error()})
				} else {
					plan.Checks = append(plan.Checks, PlanCheck{entry.File, "Empty database target", "passed", "Explicit running database target has no user tables"})
				}
			}
		}
	}
	if selected == 0 {
		plan.Ready = false
		plan.Checks = append(plan.Checks, PlanCheck{"selection", "Recovery selection", "failed", "Map at least one archive object to a new destination"})
	}
	return plan
}
func (s *RecoveryService) StartRestore(id string, request RecoveryRestoreRequest) (Operation, error) {
	if len(request.Passphrase) < 12 {
		return Operation{}, fmt.Errorf("provide the archive recovery passphrase")
	}
	job, err := s.Operations.Begin("recovery_restore", "maintenance:recovery", map[string]any{"archive": id, "instance_volume": request.InstanceVolume, "volumes": request.Volumes, "projects": request.Projects})
	if err != nil {
		return job, err
	}
	go func() {
		err := s.restore(id, request, job)
		request.Passphrase = ""
		_ = s.Operations.Finish(job.ID, err)
	}()
	return job, nil
}
func (s *RecoveryService) restore(id string, request RecoveryRestoreRequest, job Operation) (result error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	manifest, directory, err := s.unpack(id, request.Passphrase)
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	plan := s.restorePlan(ctx, manifest, request)
	if !plan.Ready {
		return fmt.Errorf("recovery preflight failed; no destinations changed")
	}
	core, err := selfContainer(ctx)
	if err != nil {
		return err
	}
	created := []string{}
	success := false
	dataChanged := false
	defer func() {
		if !success {
			cleanup, done := context.WithTimeout(context.Background(), time.Minute)
			defer done()
			for _, volume := range created {
				if owned, e := inspectRecoveryVolume(cleanup, volume); e == nil && owned.Labels["io.docktun.recovery"] == job.ID {
					if _, e = updateDocker(cleanup, "volume", "rm", volume); e != nil {
						result = &operationRecoveryRequired{fmt.Errorf("new recovery volume cleanup failed; inspect resources")}
					}
				}
			}
		}
		if !success && dataChanged {
			result = &operationRecoveryRequired{fmt.Errorf("restore stopped after changing a new project/database; inspect the selected destinations before retrying")}
		}
	}()
	for _, entry := range manifest.Entries {
		source := filepath.Join(directory, strings.ReplaceAll(entry.File, "/", "-"))
		target := ""
		if entry.Kind == "volume" {
			target = request.Volumes[entry.Name]
		}
		if entry.Kind == "instance" {
			target = request.InstanceVolume
		}
		if target != "" {
			// Recheck immediately before creation. Labels prove ownership for cleanup.
			if _, e := inspectRecoveryVolume(ctx, target); e == nil {
				return fmt.Errorf("recovery destination appeared after preflight")
			} else if !recoveryVolumeMissing(e) {
				return fmt.Errorf("cannot verify recovery destination")
			}

			if _, e := updateDocker(ctx, "volume", "create", "--label", "io.docktun.recovery="+job.ID, target); e != nil {
				return e
			}
			owned, e := inspectRecoveryVolume(ctx, target)
			if e != nil || owned.Labels["io.docktun.recovery"] != job.ID {
				return fmt.Errorf("volume ownership could not be proven; existing data was not touched")
			}
			created = append(created, target)
			resources := []string{}
			for _, name := range created {
				resources = append(resources, "new-volume:"+name)
			}
			if err := s.Operations.MergeSnapshot(job.ID, map[string]any{"resources": resources}); err != nil {
				return err
			}
			input, e := os.Open(source)
			if e != nil {
				return e
			}
			cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-i", "--network", "none", "--mount", "type=volume,source="+target+",target=/restore", "--entrypoint", "/app/server", core.Image, "recovery-volume-extract", "/restore")
			cmd.Stdin = input
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			e = cmd.Run()
			input.Close()
			if e != nil {
				return fmt.Errorf("new recovery volume extraction failed")
			}
			if entry.Kind == "instance" {
				if _, e = updateDocker(ctx, "run", "--rm", "--network", "none", "--mount", "type=volume,source="+target+",target=/restore", "--entrypoint", "/app/server", core.Image, "recovery-instance-check", "/restore/app.db"); e != nil {
					return fmt.Errorf("restored instance database failed its integrity check")
				}
			}
		}
		if entry.Kind == "project" && request.Projects[entry.Name] != "" {
			input, e := os.Open(source)
			if e != nil {
				return e
			}
			newName := filepath.Base(request.Projects[entry.Name])
			dataChanged = true
			_, e = NewProjects(s.DB, s.Config.SelfPath).Restore(newName, request.Projects[entry.Name], input)
			input.Close()
			if e != nil {
				return e
			}
		}
		if entry.Kind == "database" {
			for _, target := range request.Databases {
				if target.Entry != entry.File {
					continue
				}
				dataChanged = true
				if err = restoreDatabase(ctx, entry, target, source); err != nil {
					return fmt.Errorf("database restore failed; new target may contain partial schema and must be inspected before retry")
				}
			}
		}
	}
	if _, err = s.DB.Exec(`UPDATE recovery_backups SET restored_at=? WHERE id=?`, time.Now().Unix(), id); err != nil {
		return err
	}
	success = true
	return nil
}
func databaseClientArgs(entry RecoveryEntry, target RecoveryDatabaseTarget, query string) []string {
	if entry.Engine == "postgres" {
		return []string{"exec", target.Container, "sh", "-c", `if [ -z "${PGPASSWORD:-}" ]; then if [ -n "${POSTGRES_PASSWORD_FILE:-}" ]; then PGPASSWORD=$(cat "$POSTGRES_PASSWORD_FILE") || exit 1; else PGPASSWORD=${POSTGRES_PASSWORD:-}; fi; fi; export PGPASSWORD; exec psql -At --username="$1" --dbname="$2" --command="$3"`, "recovery", target.User, target.Database, query}
	}
	binary := "mysql"
	if entry.Engine == "mariadb" {
		binary = "mariadb"
	}
	return []string{"exec", target.Container, "sh", "-c", `if [ -z "${MYSQL_PWD:-}" ]; then if [ "$2" = root ]; then password=${MYSQL_ROOT_PASSWORD:-${MARIADB_ROOT_PASSWORD:-}}; password_file=${MYSQL_ROOT_PASSWORD_FILE:-${MARIADB_ROOT_PASSWORD_FILE:-}}; else password=${MYSQL_PASSWORD:-${MARIADB_PASSWORD:-}}; password_file=${MYSQL_PASSWORD_FILE:-${MARIADB_PASSWORD_FILE:-}}; fi; if [ -n "$password_file" ]; then password=$(cat "$password_file") || exit 1; fi; MYSQL_PWD=$password; fi; export MYSQL_PWD; exec "$1" --batch --skip-column-names --user="$2" --database="$3" --execute="$4"`, "recovery", binary, target.User, target.Database, query}
}
func validateDatabaseRestoreTarget(ctx context.Context, entry RecoveryEntry, target RecoveryDatabaseTarget) error {
	if !recoveryIdentifier.MatchString(target.Database) || !recoveryIdentifier.MatchString(target.User) {
		return fmt.Errorf("explicit database and account names are required")
	}
	container, err := inspectUpdateContainer(ctx, target.Container)
	if err != nil || !container.State.Running {
		return fmt.Errorf("selected database target is unavailable")
	}
	engine, major, err := databaseContainerVersion(ctx, container)
	if err != nil {
		return err
	}
	if engine != entry.Engine || major != entry.Major {
		return fmt.Errorf("restore requires the same verified database engine and major version; perform migrations separately")
	}

	query := "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')"
	if entry.Engine != "postgres" {
		query = "SELECT count(*) FROM information_schema.tables WHERE table_schema = DATABASE()"
	}
	raw, err := updateDocker(ctx, databaseClientArgs(entry, target, query)...)
	if err != nil || strings.TrimSpace(string(raw)) != "0" {
		return fmt.Errorf("target database must be empty and readable by the selected account")
	}
	return nil
}
func restoreDatabase(ctx context.Context, entry RecoveryEntry, target RecoveryDatabaseTarget, source string) error {
	if err := validateDatabaseRestoreTarget(ctx, entry, target); err != nil {
		return err
	}
	args := []string{"exec", "-i", target.Container, "sh", "-c"}
	switch entry.Engine {
	case "postgres":
		args = append(args, `if [ -z "${PGPASSWORD:-}" ]; then if [ -n "${POSTGRES_PASSWORD_FILE:-}" ]; then PGPASSWORD=$(cat "$POSTGRES_PASSWORD_FILE") || exit 1; else PGPASSWORD=${POSTGRES_PASSWORD:-}; fi; fi; export PGPASSWORD; exec pg_restore --exit-on-error --single-transaction --no-owner --no-acl --username="$1" --dbname="$2"`, "recovery", target.User, target.Database)
	case "mysql", "mariadb":
		binary := "mysql"
		if entry.Engine == "mariadb" {
			binary = "mariadb"
		}
		args = append(args, `if [ -z "${MYSQL_PWD:-}" ]; then if [ "$2" = root ]; then password=${MYSQL_ROOT_PASSWORD:-${MARIADB_ROOT_PASSWORD:-}}; password_file=${MYSQL_ROOT_PASSWORD_FILE:-${MARIADB_ROOT_PASSWORD_FILE:-}}; else password=${MYSQL_PASSWORD:-${MARIADB_PASSWORD:-}}; password_file=${MYSQL_PASSWORD_FILE:-${MARIADB_PASSWORD_FILE:-}}; fi; if [ -n "$password_file" ]; then password=$(cat "$password_file") || exit 1; fi; MYSQL_PWD=$password; fi; export MYSQL_PWD; exec "$1" --user="$2" --database="$3"`, "recovery", binary, target.User, target.Database)
	default:
		return fmt.Errorf("unsupported database engine")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = input
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

type recoveryVolumeInfo struct{ Labels map[string]string }

func inspectRecoveryVolume(ctx context.Context, name string) (recoveryVolumeInfo, error) {
	var result recoveryVolumeInfo
	err := dockerJSON(ctx, "GET", "/volumes/"+url.PathEscape(name), nil, &result)
	return result, err
}
func recoveryVolumeMissing(err error) bool {
	var api *DockerAPIError
	return errors.As(err, &api) && api.Status == 404
}
