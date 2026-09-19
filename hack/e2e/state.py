"""Summarise NodeClaim/Node state for the e2e assertions. Reads `kubectl get -o json` on stdin."""
import json
import sys

KBELT = "bestbefore.k-belt.io"
MODE = sys.argv[1]
items = json.load(sys.stdin)["items"]


def conditions(nc):
    return {c["type"]: c["status"] for c in nc.get("status", {}).get("conditions", [])}


if MODE == "summary":  # one line of counts
    drifted = [n for n in items if conditions(n).get("Drifted") == "True"]
    disrupting = [n for n in items if conditions(n).get("DisruptionReason") == "True"]
    marked = [n for n in items if f"{KBELT}/policy" in n["metadata"].get("annotations", {})]
    print(f"{len(items)} {len(marked)} {len(drifted)} {len(disrupting)}")
elif MODE == "names":
    print(" ".join(sorted(n["metadata"]["name"] for n in items)))
elif MODE == "marked":
    print(" ".join(sorted(n["metadata"]["name"] for n in items
                          if f"{KBELT}/policy" in n["metadata"].get("annotations", {}))))
elif MODE == "annotation-keys":
    print(" ".join(sorted({k for n in items for k in n["metadata"].get("annotations", {}) if KBELT in k})))
elif MODE == "taints":  # nodes carrying k-belt's drifted taint
    print(sum(1 for n in items for t in n["spec"].get("taints", []) if KBELT in t["key"]))
else:
    sys.exit(f"unknown mode {MODE}")
