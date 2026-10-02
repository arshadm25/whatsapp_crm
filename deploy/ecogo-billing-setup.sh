#!/usr/bin/env zsh
# Switch on billing: Razorpay keys (stored in the cluster secret, never printed) and the seller
# details printed on GST invoices. Safe to re-run. Press Enter to keep a value you do not have yet.
set -eu
KC="/Users/admin/Projects/Phase 2 - Travel/kubeconfig-hetzner.yml"
k() { kubectl --kubeconfig "$KC" "$@"; }
h() { helm --kubeconfig "$KC" "$@"; }
NS=ecogo-whatsapp

read -s "RZP_ID?Razorpay Key ID (input hidden, Enter to skip): "; echo
read -s "RZP_SECRET?Razorpay Key Secret (hidden): "; echo
read -s "RZP_HOOK?Razorpay webhook secret (hidden; the one you type in Razorpay's webhook form): "; echo
if [ -n "$RZP_ID$RZP_SECRET$RZP_HOOK" ]; then
  for pair in "razorpay-key-id:$RZP_ID" "razorpay-key-secret:$RZP_SECRET" "razorpay-webhook-secret:$RZP_HOOK"; do
    key=${pair%%:*}; val=${pair#*:}
    [ -n "$val" ] && k -n $NS patch secret ecogo-whatsapp --type merge -p "{\"stringData\":{\"$key\":\"$val\"}}" >/dev/null
  done
  echo "Razorpay values stored."
fi
unset RZP_ID RZP_SECRET RZP_HOOK

read "GSTIN?Seller GSTIN (15 characters): "
read "SAC?SAC code printed on invoices (ask your accountant, often 998439 or 998314): "
read "ADDR?Registered address printed on invoices (one line): "
VALS=$(mktemp)
q() { printf "%s" "$1" | sed "s/'/''/g"; }
cat > "$VALS" <<YAML
config:
  sellerGstin: '$(q "$GSTIN")'
  sellerSac: '$(q "$SAC")'
  sellerAddress: '$(q "$ADDR")'
YAML
cd "$(dirname "$0")/.."
h upgrade ecogo-whatsapp deploy/helm/ecogo-whatsapp -n $NS --reuse-values -f "$VALS"
rm -f "$VALS"
k -n $NS rollout restart deploy/ecogo-whatsapp-api deploy/ecogo-whatsapp-worker

cat <<'TXT'

Still to do by hand:
 1. Razorpay dashboard > Webhooks: URL https://whatsapp.ecogo.co.in/webhooks/razorpay, the same secret as above,
    all subscription.* events (authenticated, activated, charged, resumed, updated, pending, halted, cancelled, completed, expired).
 2. In Razorpay create a monthly plan per Ecogo plan and one for the extra-seat price.
 3. Admin console > Plans: paste each Razorpay plan ID and the extra-seat plan ID.
TXT
