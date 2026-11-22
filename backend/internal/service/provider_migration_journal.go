package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const connectorMigrationLabel = "io.docktun.connector-migration"

type connectorNetwork struct {
	Name, ID, IPv4, IPv6 string
	Aliases, LinkLocal   []string
	Priority             int
}

func (network connectorNetwork) static() bool {
	return network.IPv4 != "" || network.IPv6 != "" || len(network.LinkLocal) > 0
}

func (network connectorNetwork) connectArgs(container string) []string {
	args := []string{"network", "connect"}
	for _, pair := range []struct{ flag, value string }{{"--ip", network.IPv4}, {"--ip6", network.IPv6}} {
		if pair.value != "" {
			args = append(args, pair.flag, pair.value)
		}
	}
	for _, ip := range network.LinkLocal {
		args = append(args, "--link-local-ip", ip)
	}
	if network.Priority != 0 {
		args = append(args, "--gw-priority", strconv.Itoa(network.Priority))
	}
	for _, alias := range network.Aliases {
		args = append(args, "--alias", alias)
	}
	return append(args, network.ID, container)
}

type connectorMigration struct {
	ID, OriginalID, Name, Backup string
	Running, Committed           bool
	Networks                     []connectorNetwork
}

func (p *ProviderContainers) migrationPath() (string, error) {
	if p.cfg.DBPath == "" {
		return "", fmt.Errorf("connector migration requires a persistent database directory")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(p.cfg.DBPath), "cloudflare-migration.json"))
}

// The snapshot contains network configuration only. Credentials remain in their
// existing mounts and never enter the migration journal or Docker labels.
func (p *ProviderContainers) saveConnectorMigration(job connectorMigration, initial bool) error {
	file, err := p.migrationPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(file), ".connector-migration-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if err := json.NewEncoder(temporary).Encode(job); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if initial {
		// Link publishes a fully written snapshot without replacing an
		// interrupted migration's durable recovery information.
		err = os.Link(temporary.Name(), file)
	} else {
		err = os.Rename(temporary.Name(), file)
	}
	if err != nil {
		return err
	}
	return syncConnectorDirectory(filepath.Dir(file))
}

func syncConnectorDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func (p *ProviderContainers) clearConnectorMigration() error {
	file, err := p.migrationPath()
	if err != nil {
		return err
	}
	if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncConnectorDirectory(filepath.Dir(file))
}

func containerMissing(err error) bool {
	return err != nil && (strings.Contains(strings.ToLower(err.Error()), "no such") || strings.Contains(strings.ToLower(err.Error()), "not found"))
}

func (p *ProviderContainers) recoverCloudflareMigration(ctx context.Context) error {
	file, err := p.migrationPath()
	if err != nil {
		// Tools that only resolve Docker networks do not require provider state.
		// A real migration still requires a writable journal before any mutation.
		return nil
	}
	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var job connectorMigration
	if json.Unmarshal(raw, &job) != nil || !regexp.MustCompile(`^[a-f0-9]{12,64}$`).MatchString(job.OriginalID) || job.ID == "" || job.Name == "" {
		return fmt.Errorf("connector migration journal is invalid; inspect %s before retrying", file)
	}
	return p.restoreConnectorMigration(ctx, job)
}

// Recovery identifies the original by immutable ID and a replacement by its
// unique migration label. A reused container name never authorizes deletion.
func (p *ProviderContainers) restoreConnectorMigration(ctx context.Context, job connectorMigration) error {
	var original updateContainer
	if !job.Committed {
		var err error
		original, err = p.inspect(ctx, job.OriginalID)
		if err != nil || original.ID != job.OriginalID {
			return fmt.Errorf("cannot recover original connector %s; replacement retained: %v", job.OriginalID, err)
		}
	}
	current, err := p.inspect(ctx, job.Name)
	if err != nil && !containerMissing(err) {
		return err
	}
	if err == nil && current.ID != job.OriginalID {
		if containerLabels(current)[connectorMigrationLabel] != job.ID {
			return fmt.Errorf("connector name %s now belongs to another container; original %s retained", job.Name, job.OriginalID)
		}
		if job.Committed {
			if _, err := p.run(ctx, "rm", job.OriginalID); err != nil && !containerMissing(err) {
				return err
			}
			return p.clearConnectorMigration()
		}
		if _, err := p.run(ctx, "rm", "-f", current.ID); err != nil {
			return err
		}
	} else if job.Committed {
		return fmt.Errorf("committed connector replacement is unavailable; original %s retained for recovery", job.OriginalID)
	}
	var restoreErrors []error
	for _, network := range job.Networks {
		if !network.static() {
			continue
		}
		attached := false
		for name, endpoint := range original.NetworkSettings.Networks {
			attached = attached || name == network.Name || endpoint["NetworkID"] == network.ID
		}
		if !attached {
			if _, err := p.run(ctx, network.connectArgs(job.OriginalID)...); err != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("restore network %s: %w", network.Name, err))
			}
		}
	}
	if err := errors.Join(restoreErrors...); err != nil {
		return fmt.Errorf("original connector %s retained; network restoration needs retry: %w", job.OriginalID, err)
	}
	if strings.TrimPrefix(original.Name, "/") != job.Name {
		if _, err := p.run(ctx, "rename", job.OriginalID, job.Name); err != nil {
			return err
		}
	}
	if job.Running && !original.State.Running {
		if _, err := p.run(ctx, "start", job.OriginalID); err != nil {
			return err
		}
	}
	return p.clearConnectorMigration()
}

func (p *ProviderContainers) connectorNetworks(ctx context.Context, old updateContainer) ([]connectorNetwork, string, error) {
	mode, _ := old.HostConfig["NetworkMode"].(string)
	if target, shared := strings.CutPrefix(mode, "container:"); shared {
		namespace, err := p.inspect(ctx, target)
		if err != nil || !namespace.State.Running || namespace.ID == "" {
			return nil, "", fmt.Errorf("connector network namespace target is unavailable or stopped")
		}
		return nil, "container:" + namespace.ID, nil
	}
	if mode == "host" || mode == "none" {
		return nil, mode, nil
	}
	var networks []connectorNetwork
	names := make([]string, 0, len(old.NetworkSettings.Networks))
	for name := range old.NetworkSettings.Networks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		endpoint := old.NetworkSettings.Networks[name]
		network := connectorNetwork{Name: name}
		network.ID, _ = endpoint["NetworkID"].(string)
		if network.ID == "" {
			network.ID = name
		}
		if priority, ok := endpoint["GwPriority"].(float64); ok {
			network.Priority = int(priority)
		}
		if ipam, ok := endpoint["IPAMConfig"].(map[string]any); ok {
			network.IPv4, _ = ipam["IPv4Address"].(string)
			network.IPv6, _ = ipam["IPv6Address"].(string)
			network.LinkLocal = interfaceStrings(ipam["LinkLocalIPs"])
		}
		for _, alias := range interfaceStrings(endpoint["Aliases"]) {
			if alias != "" && alias != old.ID && (len(old.ID) < 12 || alias != old.ID[:12]) {
				network.Aliases = append(network.Aliases, alias)
			}
		}
		networks = append(networks, network)
	}
	for i, network := range networks {
		if network.Name == mode || network.ID == mode {
			networks[0], networks[i] = networks[i], networks[0]
			break
		}
	}
	for _, network := range networks {
		if network.Priority == 0 {
			continue
		}
		version, err := p.run(ctx, "version", "--format", "{{.Server.APIVersion}}")
		var major, minor int
		_, parseErr := fmt.Sscanf(strings.TrimSpace(string(version)), "%d.%d", &major, &minor)
		if forced := os.Getenv("DOCKER_API_VERSION"); forced != "" {
			_, parseErr = fmt.Sscanf(forced, "%d.%d", &major, &minor)
		}
		if err != nil || parseErr != nil || major < 1 || major == 1 && minor < 48 {
			return nil, "", fmt.Errorf("preserving connector gateway priority requires Docker API 1.48 or newer")
		}
		help, err := p.run(ctx, "network", "connect", "--help")
		if err != nil || !strings.Contains(string(help), "--gw-priority") {
			return nil, "", fmt.Errorf("preserving connector gateway priority requires a Docker CLI with gateway-priority support")
		}
		break
	}
	return networks, mode, nil
}

func migrationCreateArgs(args []string, old updateContainer, networks []connectorNetwork, mode, id string) []string {
	options := []string{"--label", connectorMigrationLabel + "=" + id}
	primary := "none"
	if mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:") {
		primary = mode
	} else if len(networks) > 0 {
		first := networks[0]
		primary = first.ID
		if first.Priority != 0 {
			primary = "name=" + primary + ",gw-priority=" + strconv.Itoa(first.Priority)
		}
		for _, pair := range []struct{ flag, value string }{{"--ip", first.IPv4}, {"--ip6", first.IPv6}} {
			if pair.value != "" {
				options = append(options, pair.flag, pair.value)
			}
		}
		for _, ip := range first.LinkLocal {
			options = append(options, "--link-local-ip", ip)
		}
		for _, alias := range first.Aliases {
			options = append(options, "--network-alias", alias)
		}
	}
	shared := strings.HasPrefix(mode, "container:")
	for _, setting := range []struct{ key, flag string }{{"Dns", "--dns"}, {"DnsSearch", "--dns-search"}, {"DnsOptions", "--dns-option"}, {"ExtraHosts", "--add-host"}} {
		if shared {
			break
		}
		for _, value := range interfaceStrings(old.HostConfig[setting.key]) {
			options = append(options, setting.flag, value)
		}
	}
	result := []string{args[0]}
	for i := 1; i < len(args); i++ {
		flag := args[i]
		if flag == "--network" && i+1 < len(args) {
			result = append(result, flag, primary)
			i++
			continue
		}
		if i+1 < len(args) && (shared && (flag == "--add-host" || flag == "--hostname" || flag == "--dns" || flag == "--dns-search" || flag == "--dns-option") || flag == "--add-host" && len(interfaceStrings(old.HostConfig["ExtraHosts"])) > 0 && args[i+1] == "host.docker.internal:host-gateway") {
			i++
			continue
		}
		result = append(result, flag)
	}
	withOptions := append([]string{result[0]}, options...)
	return append(withOptions, result[1:]...)
}

func (p *ProviderContainers) recreateCloudflare(ctx context.Context, old updateContainer, args []string) error {
	if !regexp.MustCompile(`^[a-f0-9]{12,64}$`).MatchString(old.ID) || len(args) == 0 {
		return fmt.Errorf("original connector identity or replacement definition is invalid")
	}
	networks, mode, err := p.connectorNetworks(ctx, old)
	if err != nil {
		return err
	}
	job := connectorMigration{ID: uuid.NewString(), OriginalID: old.ID, Name: p.name("cloudflare"), Running: old.State.Running, Networks: networks}
	job.Backup = job.Name + "-auth-backup-" + job.ID
	if err := p.saveConnectorMigration(job, true); err != nil {
		return err
	}
	rollback := func(cause error) error {
		restore, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := p.restoreConnectorMigration(restore, job); err != nil {
			return fmt.Errorf("connector migration failed (%v); recovery needs retry: %w", cause, err)
		}
		return fmt.Errorf("connector migration failed; original restored: %w", cause)
	}
	if old.State.Running {
		if _, err := p.run(ctx, "stop", old.ID); err != nil {
			return rollback(err)
		}
	}
	if _, err := p.run(ctx, "rename", old.ID, job.Backup); err != nil {
		return rollback(err)
	}
	for _, network := range networks {
		if network.static() {
			if _, err := p.run(ctx, "network", "disconnect", "--force", network.ID, old.ID); err != nil {
				return rollback(err)
			}
		}
	}
	if _, err := p.run(ctx, migrationCreateArgs(args, old, networks, mode, job.ID)...); err != nil {
		return rollback(err)
	}
	for _, network := range networks[min(1, len(networks)):] {
		if _, err := p.run(ctx, network.connectArgs(job.Name)...); err != nil {
			return rollback(err)
		}
	}
	if _, err := p.run(ctx, "start", job.Name); err != nil {
		return rollback(err)
	}
	replacement, err := p.inspect(ctx, job.Name)
	if err != nil || !replacement.State.Running || containerLabels(replacement)[connectorMigrationLabel] != job.ID {
		return rollback(fmt.Errorf("replacement did not reach running state with the expected migration identity"))
	}
	job.Committed = true
	if err := p.saveConnectorMigration(job, false); err != nil {
		return fmt.Errorf("replacement started; migration finalization needs retry: %w", err)
	}
	return p.restoreConnectorMigration(ctx, job)
}
