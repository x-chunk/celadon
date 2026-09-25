# Configuration and keys

## Where things are kept

Everything celadon keeps is under one directory: `~/.celadon`, or
`$CELADON_HOME` when it is set.

```text
~/.celadon/
├── config.toml        profiles and the default one — nothing secret
├── keys/              mode 0700
│   ├── default        one application key per file, mode 0600
│   └── work
├── admin/             mode 0700
│   └── ops            one deployment ADMIN_TOKEN per file, mode 0600
└── metrics/           mode 0700
    └── ops            one METRICS_TOKEN per file, mode 0600
```

`config.toml` looks like this, and is safe to open, share or sync:

```toml
# Written by celadon. Keys are not kept here: they are in keys/<profile>.

default_profile = "work"

[profiles.default]
base_url = "http://144.31.187.78:8080"
app_id = 7
app_name = "Reports"
key_prefix = "aek_AbCdEfGh"

[profiles.work]
base_url = "https://aether.example.com"
auth_header = "x-aether-key"

[profiles.ops]
base_url = "https://aether.example.com"
admin_base_url = "https://internal.example.com"
metrics_url = "http://127.0.0.1:19090"
```

`app_id`, `app_name` and `key_prefix` are what the key opened when it was
stored, so `auth status --offline` can say which application a profile is
without a request. `auth_header` is how the key is sent: `bearer` (the
default, `Authorization: Bearer …`), `bare` (`Authorization: …`) or
`x-aether-key` (`X-Aether-Key: …`), for a network that strips `Authorization`.
`admin_base_url` is where the admin API is, for a deployment whose proxy serves
`/admin` somewhere other than the Plug-In API; without it the admin API is
reached at the profile's `base_url`, which is where Aether serves both.

`metrics_url` is where the metrics listener is: its own port, published on the
deployment's loopback address, so `http://127.0.0.1:9090` by default — which is
also where `ssh -N -L 9090:127.0.0.1:9090 host` puts a remote one. It is never
derived from `base_url`.

A profile may hold an application key, an admin token and a metrics token in
any combination: `auth login`, `admin login` and `metrics login` each store
their own, and none needs the others.

## How the keys and tokens are protected

Admin and metrics tokens are kept exactly as keys are, in `admin/<profile>` and
`metrics/<profile>`, and everything below holds for all three.

- A key is read from a hidden prompt or from standard input. There is no flag
  for it: an argument is visible to every user of the machine through `ps` and
  is written into shell history.
- Each key is in a file of its own, created `0600` inside a `0700` directory,
  and written atomically (a temporary file renamed into place), so an
  interrupted write never leaves half a key.
- A key file that anyone but its owner can read is refused, the way ssh refuses
  a private key: `chmod 600 ~/.celadon/keys/<profile>` fixes it. (Windows
  permissions are ACLs and are not checked.)
- A profile name becomes a file name, so it is held to lowercase letters,
  digits, `-` and `_`: nothing a path can be built out of.
- Printed keys are masked to their first twelve characters, as the bot shows
  them, everywhere but `auth token`, whose whole purpose is to print one.

`auth logout` deletes the key file. Aether keeps no copy it can show again: the
way back is to regenerate the key in the bot, which retires the old one.

## Which profile, deployment and key a command uses

Highest precedence first:

| Setting  | Sources |
|----------|---------|
| profile  | `--profile`, `$CELADON_PROFILE`, `default_profile`, `default` |
| base URL | `--base-url`, `$CELADON_BASE_URL`, the profile's `base_url`, the built-in default |
| key      | `$CELADON_API_KEY`, `keys/<profile>` |
| admin API | `--base-url`, `$CELADON_ADMIN_BASE_URL`, the profile's `admin_base_url`, then the base URL as above |
| admin token | `$CELADON_ADMIN_TOKEN`, `admin/<profile>` |
| metrics listener | `--metrics-url`, `$CELADON_METRICS_URL`, the profile's `metrics_url`, `http://127.0.0.1:9090` |
| metrics token | `$CELADON_METRICS_TOKEN`, `metrics/<profile>`, or none |

`auth login`, `admin login` and `metrics login` store into the profile the
same rules choose,
so with a default profile of `work`, a bare `celadon auth login` replaces the
key of `work`. The
first profile ever stored becomes the default; `auth switch` or
`auth login --default` changes it.

A CI job needs no files at all:

```sh
export CELADON_API_KEY=aek_…            # from the CI's secret store
export CELADON_ADMIN_TOKEN=…            # only for `celadon admin`
export CELADON_METRICS_URL=http://127.0.0.1:9090   # only for `celadon metrics`
export CELADON_BASE_URL=https://aether.example.com
celadon search --where media=photo -o json
```

## Other environment variables

| Variable       | Effect |
|----------------|--------|
| `NO_COLOR`     | no colors in any output (also `--no-color`, `CLICOLOR=0`, `TERM=dumb`) |
| `VISUAL`, `EDITOR` | the editor `actions create/edit` opens (falls back to `vi`, or `notepad` on Windows) |
