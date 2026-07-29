// ura-analyzer: the fully offline central analyzer of the Utilization &
// Rightsizing Analyzer. Generates keys, imports encrypted bundles from many
// servers, and produces consolidated rightsizing reports. Makes no network
// calls of any kind.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bobarudragos94-wq/costoptimization/internal/analyzer"
	"github.com/bobarudragos94-wq/costoptimization/internal/crypt"
	"github.com/bobarudragos94-wq/costoptimization/internal/model"
	"github.com/bobarudragos94-wq/costoptimization/internal/synth"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Printf("ura-analyzer %s (schema %s)\n", model.AgentVersion, model.SchemaVersion)
	case "keygen":
		cmdKeygen(os.Args[2:])
	case "analyze":
		cmdAnalyze(os.Args[2:])
	case "synth":
		cmdSynth(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: ura-analyzer <command> [flags]

commands:
  keygen   --out DIR                        generate the offline keypair
  analyze  --key FILE --in DIR --out DIR    import bundles and write reports
  synth    --recipient KEY --out DIR [...]  generate a synthetic demo fleet
  version                                   print version`)
}

// cmdKeygen creates identity.txt (private, 0600 — stays on this machine) and
// recipient.txt (public — the only thing agents ever receive).
func cmdKeygen(args []string) {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "ura-keys", "output directory for the keypair")
	fs.Parse(args)

	if err := os.MkdirAll(*out, 0o700); err != nil {
		fatal(err)
	}
	idPath := filepath.Join(*out, "identity.txt")
	if _, err := os.Stat(idPath); err == nil {
		fatal(fmt.Errorf("%s already exists; refusing to overwrite a private key", idPath))
	}
	id, rec, err := crypt.GenerateIdentity()
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(idPath, []byte("# ura decryption identity — NEVER copy to monitored servers\n"+id+"\n"), 0o600); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(*out, "recipient.txt"), []byte(rec+"\n"), 0o644); err != nil {
		fatal(err)
	}
	fmt.Printf("keypair written to %s\n", *out)
	fmt.Printf("  identity.txt   PRIVATE — keep on the analyzer machine only\n")
	fmt.Printf("  recipient.txt  public  — set as agent.recipient in agent.yaml\n")
	fmt.Printf("recipient: %s\n", rec)
}

func cmdAnalyze(args []string) {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	key := fs.String("key", "", "identity file (from keygen)")
	in := fs.String("in", "", "directory containing .urab bundles")
	out := fs.String("out", "ura-reports", "output directory for reports")
	fs.Parse(args)
	if *key == "" || *in == "" {
		fs.Usage()
		os.Exit(2)
	}
	ids, err := crypt.LoadIdentities(*key)
	if err != nil {
		fatal(err)
	}
	res, err := analyzer.Run(*in, ids, *out)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("imported %d bundle(s): %d ok, %d partial, %d rejected\n",
		res.Bundles, res.OKBundles, res.PartialBundles, res.RejectedBundles)
	for _, w := range res.Warnings {
		fmt.Println("  warning:", w)
	}
	fmt.Printf("hosts analyzed: %d, SQL instances: %d, spikes: %d\n",
		res.Hosts, res.SQLInstances, res.Spikes)
	fmt.Println("reports written to", *out)
}

func cmdSynth(args []string) {
	fs := flag.NewFlagSet("synth", flag.ExitOnError)
	recipient := fs.String("recipient", "", "age recipient public key (or @file)")
	out := fs.String("out", "ura-demo-bundles", "output directory for demo bundles")
	days := fs.Int("days", 14, "days of synthetic history")
	fs.Parse(args)
	if *recipient == "" {
		fs.Usage()
		os.Exit(2)
	}
	rec := *recipient
	if rec[0] == '@' {
		b, err := os.ReadFile(rec[1:])
		if err != nil {
			fatal(err)
		}
		rec = string(b)
	}
	paths, err := synth.GenerateFleet(rec, *out, *days)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("generated %d synthetic encrypted bundles in %s\n", len(paths), *out)
	for _, p := range paths {
		fmt.Println("  ", filepath.Base(p))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
