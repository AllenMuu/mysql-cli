# @allenmuu/mysql-cli

One-line install of the [mysql-cli](https://github.com/AllenMuu/mysql-cli) Go binary for AI agents.

## Install

```bash
npx @allenmuu/mysql-cli install      # installs the binary to ~/.local/bin
mysql-cli init                       # installs agent skills (auto-detected)
```

No Go toolchain required. The `install` command downloads the prebuilt binary for your platform from GitHub Releases.

## One-shot usage (no permanent install)

```bash
npx @allenmuu/mysql-cli init         # install skills into detected agents
npx @allenmuu/mysql-cli skill check  # check installed skill versions
npx @allenmuu/mysql-cli query "SELECT 1" -d mydb
```

## Mirror (GFW-friendly)

Set `MYSQL_CLI_MIRROR` to a mirror of the GitHub Releases download path:

```bash
MYSQL_CLI_MIRROR=https://ghproxy.com/https://github.com/AllenMuu/mysql-cli/releases/download npx @allenmuu/mysql-cli install
```

## License

MIT (matches the upstream repo).