# Linode escort deployment

Deploys escorts to Linode for a flagship you already run. You get one escort per region you list, each on a Linode 2GB (`g6-standard-1`, 2GB RAM, shared CPU) with Ubuntu 24.04.
Every escort pulls the stream from the flagship you give it. The escort binary is built on your machine and runs as the `vrc-haven` systemd service. ufw only opens SSH and HLS.

Each instance is tagged `vrc-haven`, `vrc-haven-<haven_name>` and `vrc-haven-escort`.

## Setup

Run these on the machine that runs Ansible. It also needs Go to build the binary.
Install into a virtualenv, distributions like Ubuntu and Debian don't allow `pip install` system-wide.

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install ansible-core linode_api4 polling ansible-specdoc
ansible-galaxy collection install -r requirements.yml
```

Activate the virtualenv with `source .venv/bin/activate` in every new shell before running the playbooks.

Create a Linode personal access token with **Linodes: Read/Write** and **Events: Read Only**, everything else can stay at No Access. The Linode modules poll events while waiting for an instance to boot.

Create a credentials file named after your Linode username, holding the token and the flagship's passphrase. Then encrypt it:

```bash
cp credentials/example.yml credentials/<linode username>.yml
ansible-vault encrypt credentials/<linode username>.yml
```

Root on every instance gets your local SSH public key, `~/.ssh/id_ed25519.pub` by default. Pass `-e ssh_public_key_file=<path>` to use another key.

## Deploy

```bash
ansible-playbook deploy.yml --ask-vault-pass \
  -e linode_user=<linode username> \
  -e escort_regions=au-mel,eu-central \
  -e haven_flagship_address=203.0.113.7 \
  -e srt_stream_id=<the flagship's SRT_STREAM_ID>
```

Variables that end up in the escort's settings have the same name as in [`.env.example`](../../.env.example), in lower case.

| Variable                 | Setting on the escort    | Meaning                                                            |
| ------------------------ | ------------------------ | ------------------------------------------------------------------ |
| `escort_regions`         |                          | Linode region IDs, one escort each (`linode-cli regions list`)     |
| `haven_flagship_address` | `HAVEN_FLAGSHIP_ADDRESS` | Public IP or hostname of the flagship                              |
| `haven_flagship_port`    | `HAVEN_FLAGSHIP_PORT`    | The flagship's SRT port, defaults to 8890                          |
| `srt_passphrase`         | `SRT_PASSPHRASE`         | The flagship's `SRT_PASSPHRASE`, from the credentials file         |
| `srt_stream_id`          | `SRT_STREAM_ID`          | The flagship's `SRT_STREAM_ID`, defaults to `ingest`               |
| `dashboard_username`     | `DASHBOARD_USERNAME`     | From the credentials file, the dashboard is off while it's empty   |
| `dashboard_password`     | `DASHBOARD_PASSWORD`     | From the credentials file                                          |
| `hls_port`               | `HLS_PORT`               | Port viewers watch on, defaults to 8080                            |
| `dashboard_port`         | `DASHBOARD_PORT`         | Defaults to 8090                                                   |
| `haven_extra_env`        | anything                 | Any other setting from `.env.example`, as a map                    |

Labels are `<haven_name>-escort-<region>`, so you can have at most one escort per region. Pass `-e haven_name=<name>` to run escorts for more than one flagship on the same account.
ufw opens SSH and the HLS port. The dashboard port is only opened when `dashboard_password` is set, otherwise reach it with `ssh -L 8090:localhost:8090 root@<instance ip>`. The other defaults are in `group_vars/all.yml`.

Re-running the playbook is safe. It ships a new binary and config and restarts only the escorts whose files changed. A region you add gets a new escort. A region you remove keeps its escort running.

## Destroy

Deletes every instance tagged `vrc-haven-<haven_name>`. It asks before deleting anything.

```bash
ansible-playbook destroy.yml --ask-vault-pass -e linode_user=<linode username>
```
