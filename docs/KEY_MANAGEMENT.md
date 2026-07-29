# Offline Key Management

## Model

age X25519 asymmetric encryption (see docs/ARCHITECTURE.md for why).

* **identity** (`AGE-SECRET-KEY-…`) — decrypts bundles. Lives ONLY on the
  analyzer machine. 0600.
* **recipient** (`age1…`) — encrypts. This is the only key material agents
  receive (`agent.recipient` in agent.yaml). Public by definition.

## Setup workflow (per engagement)

```
# 1. On the analyzer machine (offline is fine):
ura-analyzer keygen --out ./keys-customerX
#    -> keys-customerX/identity.txt   (PRIVATE, 0600, never leaves)
#    -> keys-customerX/recipient.txt  (public)

# 2. Copy ONLY recipient.txt content into every agent.yaml:
#      agent:
#        recipient: "age1...."

# 3. During/after monitoring, collect *.urab bundles from each server's
#    export directory (USB / internal file share / scp inside the customer
#    network — the product itself never transmits).

# 4. On the analyzer machine:
ura-analyzer analyze --key ./keys-customerX/identity.txt --in ./bundles --out ./reports
```

## Rules

1. **One keypair per engagement/customer.** Compartmentalizes disclosure and
   lets the key be destroyed with the engagement.
2. The identity never touches a monitored server; installers and docs never
   ask for it. `keygen` refuses to overwrite an existing identity.
3. Back up the identity offline (printed or sealed media) if bundle retention
   matters — without it, bundles are permanently unreadable. That is the
   designed failure mode, not an accident.
4. **Rotation**: run `keygen` into a new directory, update `agent.recipient`
   on the servers (config change + service restart). During the overlap,
   append both identities (one per line) into a single key file — the
   analyzer tries each. Old bundles remain readable with the old identity.
5. **Compromise of a server**: nothing to revoke — servers hold only public
   material and cannot read any bundle, including their own.
6. **Compromise of the analyzer identity**: treat all bundles encrypted to its
   recipient as exposed; rotate, re-export what is still on servers, destroy
   the old identity.
7. End of engagement: deliver or destroy bundles per contract, then destroy
   the identity (secure-delete both copies). Uninstall scripts remove all
   server-side material.
