# kgssh

A small Go CLI for launching SSH connections from named config entries.

## Commands

### `kgssh add`

Add a new SSH connection entry to the config file.

```bash
./kgssh add <name> <user@host> [--port <port>] [--extra-arg <arg>...]
```

Examples:

```bash
./kgssh add prod ubuntu@prod.example.com --port 2222 --extra-arg '-i' --extra-arg '/path/to/id_rsa'
./kgssh add staging --user deploy --host staging.example.com --port 2200
```

### `kgssh connect`

Connect to a configured SSH server by alias.

```bash
./kgssh connect prod
```

### `kgssh list`

List all configured SSH servers.

```bash
./kgssh list
```

## Configuration

By default, kgssh stores configuration at:

```bash
~/.kgssh/config.json
```

You can override the config path with:

```bash
export KGSSH_CONFIG=~/my-config.json
```

### Example config file

```json
{
  "entries": {
    "prod": {
      "user": "ubuntu",
      "host": "prod.example.com",
      "port": 2222,
      "extraArgs": ["-i", "/path/to/id_rsa"]
    }
  }
}
```

## Build

```bash
go build -o kgssh .
```
