# TrueProxies MCP server

Connect Claude, Cursor or any MCP client to your [TrueProxies](https://trueproxies.com) account. Ask about plans and prices, your services, traffic used, live traffic, usage history and invoices, generate endpoints and check a connection.

The server cannot buy, pay, change your proxy password or change trusted IPs. Every tool only reads, except `check_connection`, which sends one request through your service.

## Tools

| Tool | What it returns | API key scope |
| --- | --- | --- |
| `list_plans` | Plans, traffic packs, prices, cities, free trial availability | none |
| `get_account` | Your account | `services:read` |
| `list_services` | Your services | `services:read` |
| `get_service` | One service: hosts, ports, capabilities, limits | `services:read` |
| `get_traffic_used` | Traffic used and remaining | `services:read` |
| `get_live_traffic` | Live download and upload speed, traffic used today | `services:read` |
| `get_usage_history` | Connection success, traffic and connection errors over 24h, 7d or 30d | `services:read` |
| `list_invoices` | Your invoices | `billing:read` |
| `get_invoice` | One invoice | `billing:read` |
| `generate_endpoints` | Proxy connection lines, including the username and password | `proxy:read` |
| `check_connection` | The result and exit IP of one request through your service. It uses a little traffic and starts an unused free trial. | `proxy:read` |
| `list_trusted_ips` | Trusted IPs of a service | `proxy:read` |

## API key

Create an API key in the [TrueProxies dashboard](https://dashboard.trueproxies.com) under API keys. For read-only use, give it `services:read` and `billing:read`. Add `proxy:read` only if the assistant should generate endpoints or check connections: endpoints contain your proxy password.

## Connect

Hosted (nothing to install):

```bash
claude mcp add --transport http trueproxies https://mcp.trueproxies.com/mcp \
  --header "Authorization: Bearer $TRUEPROXIES_API_KEY"
```

Cursor (`~/.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "trueproxies": {
      "url": "https://mcp.trueproxies.com/mcp",
      "headers": { "Authorization": "Bearer ${env:TRUEPROXIES_API_KEY}" }
    }
  }
}
```

Local, with Docker. Build the image once:

```bash
docker build -t trueproxies-mcp https://github.com/trueproxies-com/trueproxies-mcp.git
```

```json
{
  "mcpServers": {
    "trueproxies": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "-e", "TRUEPROXIES_API_KEY", "trueproxies-mcp"],
      "env": { "TRUEPROXIES_API_KEY": "tp_api_..." }
    }
  }
}
```

Local, with Go: `go install github.com/trueproxies-com/trueproxies-mcp@latest`, then run `trueproxies-mcp stdio` with `TRUEPROXIES_API_KEY` set.

## Privacy

The hosted server forwards your API key to `api.trueproxies.com` for each call and keeps nothing. It logs the tool name, status, request ID and duration, never arguments, results or keys.

## Links

- [TrueProxies](https://trueproxies.com)
- [API reference](https://docs.trueproxies.com)
- [Pricing](https://trueproxies.com/pricing)

## License

MIT
