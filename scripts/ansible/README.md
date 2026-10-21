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
  -e flagship_ip=203.0.113.7 \
  -e flagship_port=8890 \
  -e flagship_application=<stream id>
```

| Variable               | Meaning                                                        |
| ---------------------- | -------------------------------------------------------------- |
| `escort_regions`       | Linode region IDs, one escort each (`linode-cli regions list`) |
| `flagship_ip`          | Public IP or hostname of the flagship                          |
| `flagship_port`        | The flagship's SRT port, defaults to 8890                      |
| `flagship_passphrase`  | The flagship's `HAVEN_PASSPHRASE`, from the credentials file   |
| `flagship_application` | The flagship's `HAVEN_STREAM_ID`                               |

Labels are `<haven_name>-escort-<region>`, so you can have at most one escort per region. Pass `-e haven_name=<name>` to run escorts for more than one flagship on the same account.
Escorts serve HLS on port 8554. The dashboard on 8080 is only opened in the firewall when `dashboard_password` is set. The other defaults are in `group_vars/all.yml`.

Re-running the playbook is safe. It ships a new binary and config and restarts only the escorts whose files changed. A region you add gets a new escort. A region you remove keeps its escort running.

## Destroy

Deletes every instance tagged `vrc-haven-<haven_name>`. It asks before deleting anything.

```bash
ansible-playbook destroy.yml --ask-vault-pass -e linode_user=<linode username>
```
