<script setup lang="ts">
import { BookOpen } from 'lucide-vue-next'
defineProps<{ provider: 'cloudflare' | 'tailscale' | 'wireguard' }>()
</script>

<template>
  <section class="cyber-card space-y-4 text-sm text-slate-700" :aria-label="`${provider} setup guide`">
    <div class="flex items-center gap-2"><BookOpen class="h-4 w-4 text-indigo-600" /><h2 class="font-semibold text-slate-900">Setup &amp; credentials</h2></div>

    <template v-if="provider === 'cloudflare'">
      <p>A public hostname needs a Cloudflare account, a domain on Cloudflare, and a tunnel. Pick the setup that matches your tunnel.</p>
      <ol class="list-decimal space-y-2 pl-5">
        <li>Open <a href="https://dash.cloudflare.com/" target="_blank" rel="noopener noreferrer" class="guide-link">Cloudflare</a> → <strong>Networking → Tunnels</strong>. Create a cloudflared tunnel, or select an existing one. Some dashboards put this under Zero Trust → Networks → Connectors.</li>
        <li>On the connector installation screen, copy the <strong>connector token</strong>: the value after <code>--token</code> in the command. Paste only that value into the <strong>Tunnel connector token</strong> field and save it. DockTun runs the connector for you.</li>
        <li>Click <strong>Enable Cloudflare</strong>. Check that the tunnel becomes healthy in Cloudflare. Then add its public-hostname/application route in Cloudflare, such as <code>app.example.com</code> → <code>http://my-app:80</code>.</li>
        <li>The service must be reachable from the connector. Container names work on a shared Docker network. For a published host port, use a reachable host address, such as <code>http://host.docker.internal:8080</code>. <code>localhost</code> inside the connector means the connector itself.</li>
      </ol>
      <p class="rounded-lg bg-indigo-50 p-3 text-xs text-indigo-900">The connector token runs the tunnel; it cannot publish hostname routes through the API. To publish Cloudflare-managed routes from Containers → Access, save a separate API token in API credentials above with account tunnel Edit/Write and zone DNS Edit permissions. A DNS-only zone token is insufficient. You can also manage routes directly in Cloudflare without giving DockTun an API token. DockTun’s local ingress editor is for locally managed tunnels.</p>

      <details class="guide-details">
        <summary class="guide-summary">API token for publishing routes: permissions, Account ID &amp; Zone ID</summary>
        <div class="guide-body">
          <p>Create a custom token under <a href="https://dash.cloudflare.com/profile/api-tokens" target="_blank" rel="noopener noreferrer" class="guide-link">My Profile → API Tokens</a>, or use <strong>Manage account → Account API tokens</strong>. Both user and account API tokens support tunnel and DNS management when granted the required permissions. Scope the token to the tunnel’s account and the hostname’s zone:</p>
          <p class="rounded-lg bg-indigo-50 p-3 text-indigo-900"><strong>Recommended: enter the Zone ID manually to avoid adding Zone Read.</strong> Open the hostname’s domain in Cloudflare → <strong>Overview → API → Zone ID</strong>, copy that value, and paste it into DockTun’s Zone ID field above. This works with user and account API tokens. The Zone ID identifies that domain’s zone; it is different from the Account ID.</p>
          <div class="overflow-x-auto"><table class="w-full text-left text-xs"><thead><tr class="border-b border-slate-200"><th class="py-2 pr-3">DockTun feature</th><th class="py-2">Token permission</th></tr></thead><tbody>
            <tr class="border-b border-slate-100"><td class="py-2 pr-3">Read existing tunnel details and routes</td><td>Account → Cloudflare Tunnel → Read</td></tr>
            <tr class="border-b border-slate-100"><td class="py-2 pr-3">Publish or update Cloudflare-managed app routes</td><td>Account → Cloudflare Tunnel → Edit (Write)</td></tr>
            <tr class="border-b border-slate-100"><td class="py-2 pr-3">Create or update DNS records</td><td>Zone → DNS → Edit (DNS Write)</td></tr>
            <tr><td class="py-2 pr-3">Manage optional Access authentication policies</td><td>Account → Access: Apps and Policies → Edit</td></tr>
          </tbody></table></div>
          <p><strong>Write and Edit are the same permission level</strong> in Cloudflare’s token summaries and builder. Cloudflare Tunnel Write grants tunnel changes; Cloudflare One Connector: cloudflared Write and Cloudflare One Connectors Write are also accepted. Read-only tunnel access cannot publish routes.</p>
          <p class="rounded-lg bg-amber-50 p-3 text-amber-900"><strong>Account DNS Settings Write does not grant DNS record editing.</strong> The token still needs <strong>Zone → DNS → Edit (DNS Write)</strong> for the hostname’s zone, even when its Zone ID is entered manually. Tunnel Write plus account DNS settings permission alone is insufficient for DockTun’s DNS setup.</p>
          <p><strong>Zone Read is only needed if you leave Zone ID blank</strong> and want DockTun to discover it automatically. In that case, add <strong>Zone → Zone → Read</strong> for the domain. With the correct Zone ID saved, no Zone Read permission is needed.</p>
          <p>Copy the generated API token into <strong>API credentials</strong> at the top of the Cloudflare page, then save/test it. Use the API token value, rather than the Global API Key.</p>
          <p><strong>Account ID:</strong> in Cloudflare search, enter “Copy account ID”; DockTun can also detect it from the connector token. Use the domain’s <strong>Zone ID</strong> in the Zone ID field, rather than copying this Account ID into both fields. These IDs identify resources; they are separate from the API token.</p>
          <p class="flex flex-wrap gap-x-4 gap-y-1 text-xs"><a href="https://developers.cloudflare.com/fundamentals/api/get-started/create-token/" target="_blank" rel="noopener noreferrer" class="guide-link">Create a user API token ↗</a><a href="https://developers.cloudflare.com/fundamentals/api/get-started/account-owned-tokens/" target="_blank" rel="noopener noreferrer" class="guide-link">Account API tokens ↗</a><a href="https://developers.cloudflare.com/fundamentals/api/reference/permissions/" target="_blank" rel="noopener noreferrer" class="guide-link">Permission reference ↗</a><a href="https://developers.cloudflare.com/fundamentals/account/find-account-and-zone-ids/" target="_blank" rel="noopener noreferrer" class="guide-link">Find account / zone IDs ↗</a></p>
        </div>
      </details>
      <details class="guide-details">
        <summary class="guide-summary">Locally managed tunnel: credentials file &amp; DockTun ingress</summary>
        <div class="guide-body">
          <ol class="list-decimal space-y-2 pl-5">
            <li>On a machine with the official cloudflared CLI, run <code>cloudflared tunnel login</code>, then <code>cloudflared tunnel create my-apps</code>. Record the tunnel UUID and its generated UUID.json credentials file.</li>
            <li>Use <router-link to="/storage" class="guide-link">Storage</router-link> → Cloudflared Config to upload the tunnel JSON as <code>credentials.json</code>. The default connector path is <code>/etc/cloudflared/credentials.json</code>; custom mounts must use their matching container path.</li>
            <li>Choose <strong>Use local YAML credentials</strong> if a connector token is saved. In Ingress Routes, enter the tunnel UUID and credentials-file path, then save the configuration. In <router-link to="/containers" class="guide-link">Containers → Access</router-link>, choose Cloudflare and enter the hostname and container target using its <strong>internal TCP port</strong>.</li>
            <li>Create the hostname’s tunnel DNS route in Cloudflare, or use DockTun’s DNS tools with the required API permissions. Enable the connector and test the hostname. Add Cloudflare Access protection to routes that require authentication.</li>
          </ol>
          <a href="https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/local-management/create-local-tunnel/" target="_blank" rel="noopener noreferrer" class="guide-link text-xs">Official local tunnel instructions ↗</a>
        </div>
      </details>
      <details class="guide-details"><summary class="guide-summary">Connection checks</summary><div class="guide-body"><p>A stopped connector needs an explicit Enable. If credentials are rejected, check that the connector token and tunnel belong together, or that the local JSON file matches the tunnel UUID. If connected but the app fails, check the Docker network, service address, port and hostname rule. A restrictive firewall must allow the connector’s outbound tunnel traffic; see <a href="https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/get-started/create-remote-tunnel/" target="_blank" rel="noopener noreferrer" class="guide-link">Cloudflare’s connection prerequisites</a>.</p></div></details>
    </template>

    <template v-else-if="provider === 'tailscale'">
      <p>App devices use a browser sign-in link. Each app gets its own device and private address; the shared companion can stay stopped.</p>
      <ol class="list-decimal space-y-2 pl-5">
        <li>Install Tailscale on the computer or phone that will access the app, and sign in to your tailnet.</li>
        <li>Open <router-link to="/containers" class="guide-link">Containers → Manage → Access</router-link>. Choose Tailscale and click <strong>Connect this app to Tailscale</strong>.</li>
        <li>Open <strong>Sign in this app’s device</strong>, authenticate to the intended tailnet, then return and check the connection. If device approval is required, ask a tailnet administrator to approve the new app on the Machines page.</li>
        <li>Set the target to the app’s Docker name and <strong>internal TCP port</strong>, choose the port on its Tailscale device, and Save exposure. From a tailnet client, open the app’s private address and that port.</li>
      </ol>
      <p class="text-xs text-slate-600">Subnet routing, an exit node, and exposing the entire Docker network are unnecessary for this app-device flow. Tailnet access rules still control who can connect.</p>
      <details class="guide-details">
        <summary class="guide-summary">Optional auth key for the advanced shared device</summary>
        <div class="guide-body">
          <p>This version’s per-app flow uses browser sign-in. The auth-key field under <strong>Advanced: legacy shared device and routing</strong> is for the shared companion.</p>
          <ol class="list-decimal space-y-2 pl-5">
            <li>In the Tailscale admin console, open <strong>Settings → Keys</strong> and choose <strong>Generate auth key</strong>. Your account needs a role permitted to generate keys.</li>
            <li>Choose its expiry and device settings. A one-off key registers one device; use a reusable key only when you intend to register several. For a persistent shared device, keep Ephemeral off. Pre-approved is useful when device approval is enabled.</li>
            <li>Copy the <code>tskey-auth-…</code> value when generated. Start the shared companion, paste it into the advanced Auth Key field, and connect. An API access token (<code>tskey-api-…</code>) is a different credential.</li>
          </ol>
          <a href="https://tailscale.com/docs/features/access-control/auth-keys" target="_blank" rel="noopener noreferrer" class="guide-link text-xs">Official auth-key instructions ↗</a>
        </div>
      </details>
      <details class="guide-details"><summary class="guide-summary">Connection checks</summary><div class="guide-body"><p>NeedsLogin requires completing the sign-in link. Needs approval requires approval in the admin console. For a connected app that will not open, check its target TCP port, running state, tailnet access rules, and the client’s tailnet connection. Device expiry or Tailnet Lock may require additional administrator action.</p><a href="https://tailscale.com/docs/features/access-control/device-management/how-to/set-up" target="_blank" rel="noopener noreferrer" class="guide-link text-xs">Device setup and approval ↗</a></div></details>
    </template>

    <template v-else>
      <p>WireGuard uses peer keys and VPN configuration rather than a cloud API token. You can use an existing router, VPN server or client; the DockTun companion is optional.</p>
      <ol class="list-decimal space-y-2 pl-5">
        <li>On your WireGuard server/router or VPN provider, create a peer and export its client <code>.conf</code> file or QR code. Import it into the WireGuard client that will access the app.</li>
        <li>Establish the VPN and verify a recent handshake. Configure routing and firewall rules for the intended app address and port.</li>
        <li>In <router-link to="/containers" class="guide-link">Containers → Manage → Access</router-link>, choose WireGuard. Enter an address already reachable over that VPN and the reachable port. For a published Docker service, this is usually the Docker host’s <strong>VPN IP and published host port</strong>.</li>
        <li>Save exposure and test from the VPN client. DockTun records this access address; it does not create per-app WireGuard devices or automatically build VPN routes.</li>
      </ol>
      <details class="guide-details">
        <summary class="guide-summary">Where peer keys come from</summary>
        <div class="guide-body">
          <p>A router’s peer wizard or the WireGuard client can generate a private/public key pair. Exchange public keys between the two peers. Each private key stays with its own peer; a preshared key is optional and must match on both sides.</p>
          <p>For manual setup on Linux with the WireGuard tools, run <code>umask 077</code>, then <code>wg genkey | tee privatekey | wg pubkey &gt; publickey</code> to create a pair. A working configuration also needs the tunnel address, remote public key, endpoint and AllowedIPs routes.</p>
          <p>Choose AllowedIPs for the intended destinations. A full-tunnel route such as <code>0.0.0.0/0</code> changes all IPv4 routing; it is not needed just to reach one app.</p>
          <a href="https://www.wireguard.com/quickstart/" target="_blank" rel="noopener noreferrer" class="guide-link text-xs">Official key and peer setup ↗</a>
        </div>
      </details>
      <details class="guide-details">
        <summary class="guide-summary">Optional DockTun companion: configuration &amp; host support</summary>
        <div class="guide-body">
          <p>The Docker host needs WireGuard kernel support. Check your NAS/vendor instructions first; DockTun does not install kernel modules. An existing VPN gateway is an alternative.</p>
          <p>Use <router-link to="/storage" class="guide-link">Storage</router-link> → DockTun Data → <code>wireguard</code> for the companion’s persistent configuration. This directory appears as <code>/config</code> inside the companion; tunnel files belong under <code>wg_confs/</code>.</p>
          <p>The companion’s environment must match its role. LinuxServer client mode requires <strong>PEERS unset</strong> and an imported <code>wg_confs/name.conf</code>. Server mode needs intentional PEERS and SERVERURL settings. Review its Compose/environment in Containers before importing a client configuration; the staged companion’s server settings are not a generic client importer.</p>
          <p>After configuring it, click <strong>Enable WireGuard</strong>, then check its logs and handshake. Hosting a server may require forwarding the configured UDP port on your router; using an outbound VPN client has different requirements.</p>
          <a href="https://docs.linuxserver.io/images/docker-wireguard/" target="_blank" rel="noopener noreferrer" class="guide-link text-xs">LinuxServer server/client setup and host requirements ↗</a>
        </div>
      </details>
      <details class="guide-details"><summary class="guide-summary">Connection checks</summary><div class="guide-body"><p>A running container does not guarantee a working tunnel. Check for a recent peer handshake, the correct keys and endpoint, UDP reachability, AllowedIPs and firewall rules. Then check the app’s published port and VPN return route. Imported configurations and QR codes contain private keys; keep them private.</p></div></details>
    </template>
  </section>
</template>

<style scoped>
.guide-link { @apply text-indigo-700 underline underline-offset-2; }
.guide-details { @apply rounded-lg border border-slate-200; }
.guide-summary { @apply cursor-pointer px-3 py-2 font-medium text-slate-800; }
.guide-body { @apply space-y-3 border-t border-slate-100 p-3 text-sm; }
code { @apply break-words rounded bg-slate-100 px-1 py-0.5 text-xs; }
</style>
