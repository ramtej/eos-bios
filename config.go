package main

import (
	"fmt"
	"io/ioutil"
	"net/url"
	"strings"

	"github.com/eoscanada/eos-go/ecc"
	"github.com/eoscanada/eos-go/system"
)

type Config struct {
	Contracts map[string]ContractLocation `json:"contracts"`

	// OpeningBalancesSnapshotPath represents the `snapshot.csv` file,
	// which holds the opening balances for all ERC-20 token holders.
	OpeningBalances struct {
		// SnapshotPath is the path to the `csv` file, extracted using the `genesis` tool.
		SnapshotPath string `json:"snapshot_path"`
	} `json:"opening_balances"`

	// Producer describes your producing node.
	Producer struct {
		// MyAccount is the name of the `account_name` this producer will be using on chain
		MyAccount string `json:"my_account"`
		// APIAddress is the target API endpoint for the locally booting node, a clean-slate node. It can be routable only from the local machine.
		APIAddress    string `json:"api_address"`
		apiAddressURL *url.URL
		// SecretP2PAddress is the endpoint which will be published at the end of the process. Needs to be externally routable.  It must be kept secret for DDoS protection.
		SecretP2PAddress string `json:"secret_p2p_address"`

		// Key you want to register to sign blocks.
		BlockSigningPublicKey ecc.PublicKey `json:"block_signing_public_key"`

		// Private key used to setup your config.ini as an ABP (or as someone joining the network)
		BlockSigningPrivateKeyPath string `json:"block_signing_private_key_path"`

		// Available once loaded successfuly from the previous field's path.
		blockSigningPrivateKey *ecc.PrivateKey
	} `json:"producer"`

	MyParameters system.EOSIOParameters `json:"my_parameters"`

	// PGP manages the PGP keys, used for the communications channel.
	PGP struct {
		// Program represents the type of program to use (gpg, keybase ?)
		Program string `json:"program"`
		// Path to binary executable.. unless we use in-process cryptography..
		Path string `json:"path"`
	} `json:"pgp"`

	// Hooks are called at different stages in the process, for
	// remote systems to be notified and act.  They are simply `http`
	// endpoints to which a POST will be sent with pre-defined structs
	// as JSON.  See `hooks.go`
	Hooks map[string]*HookConfig `json:"hooks"`

	// This must all be empty for production.
	Debug struct {
		// EnrichProducer will distribute coins to all producers in LaunchData.
		EnrichProducers bool `json:"enrich_producers"`
		// KeepSystemAccount prevents the destrution of the `eosio` account and removal of its keys.
		KeepSystemAccount bool `json:"keep_system_account"`
		// NoShuffle prevents shuffling of the Producers in your
		// LaunchData. Helps tests different roles (BIOS Boot Node,
		// ABP, watcher...)
		NoShuffle bool `json:"no_shuffle"`
		// Truncate snapshot
		TruncateSnapshot int `json:"truncate_snapshot"`
	}
}

type ContractLocation struct {
	CodePath string `json:"code_path"`
	ABIPath  string `json:"abi_path"`
}

type HookConfig struct {
	URL  string `json:"url"`
	Exec string `json:"exec"`
	Wait bool   `json:"wait"`
}

func LoadLocalConfig(localConfigPath string) (*Config, error) {
	cnt, err := ioutil.ReadFile(localConfigPath)
	if err != nil {
		return nil, err
	}

	var c *Config
	if err = yamlUnmarshal(cnt, &c); err != nil {
		return nil, err
	}

	// TODO: do more checks on configuration...
	// TODO: test all Webhook URLs if defined
	// TODO: test all Hooks's Exec templates, and compile them right away..
	h := c.Hooks
	fmt.Println("Hooks runtime config (see `hooks.go`):")
	for _, hook := range configuredHooks {
		hconf := h[hook.Key]
		if hconf == nil {
			fmt.Printf("Hook %q NOT configured\n", hook.Key)
			continue
		}

		if hconf.Exec != "" {
			fmt.Printf("Hook %q configured to EXEC\n", hook.Key)
		}
		if hconf.URL != "" {
			fmt.Printf("Hook %q configured to POST via HTTP\n", hook.Key)
		}
	}

	c.Producer.apiAddressURL, err = url.Parse(c.Producer.APIAddress)
	if err != nil {
		return c, err
	}

	privKey, err := ioutil.ReadFile(c.Producer.BlockSigningPrivateKeyPath)
	if err != nil {
		return c, err
	}

	wif, err := ecc.NewPrivateKey(strings.TrimSpace(string(privKey)))
	if err != nil {
		return c, err
	}

	c.Producer.blockSigningPrivateKey = wif

	return c, nil
}

/*

Default values in code for eosio_parameters:

static const uint32_t bandwidth_average_window_ms   = 24*60*60*1000l;
static const uint32_t compute_average_window_ms     = 24*60*60*1000l;
static const uint32_t blocksize_average_window_ms   = 60*1000l;


const static uint32_t   default_max_block_size              = 1024 * 1024; /// at 500ms blocks and 200byte trx, this enables 10,000 TPS burst
const static uint32_t   default_target_block_size           = default_max_block_size / 10; /// we target 1000 TPS burst
const static uint32_t   default_target_block_acts_per_scope  = 1000;
const static uint32_t   default_max_block_acts_per_scope     = default_target_block_acts_per_scope*10;

const static uint32_t   default_target_block_acts  = 2000;
const static uint32_t   default_max_block_acts     = default_target_block_acts*100;
const static uint32_t   setcode_act_usage          = 100;

const static uint64_t   default_max_storage_size       = 10 * 1024;
const static uint32_t   default_max_trx_lifetime       = 60*60;
const static uint16_t   default_max_auth_depth         = 6;
const static uint32_t   default_max_trx_runtime        = 10*1000;
const static uint16_t   default_max_inline_depth       = 4;
const static uint32_t   default_max_inline_action_size = 4 * 1024;
const static uint32_t   default_max_gen_trx_size       = 64 * 1024; ///
const static uint32_t   default_max_gen_trx_count      = 16; ///< the number of generated transactions per action
const static uint32_t   producers_authority_threshold  = 14;
const static uint32_t   rate_limiting_precision        = 1000*1000;

const static uint16_t   max_recursion_depth = 6;
*/
