package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/eoscanada/eos-go"
	"github.com/eoscanada/eos-go/ecc"
	"github.com/eoscanada/eos-go/system"
)

type BIOS struct {
	LaunchData   *LaunchData
	Config       *Config
	API          *eos.API
	Snapshot     Snapshot
	ShuffleBlock struct {
		Time       time.Time
		MerkleRoot []byte
	}
	ShuffledProducers []*ProducerDef
	MyProducerDefs    []*ProducerDef

	EphemeralPrivateKey *ecc.PrivateKey
}

func NewBIOS(launchData *LaunchData, config *Config, snapshotData Snapshot, api *eos.API) *BIOS {
	b := &BIOS{
		LaunchData: launchData,
		Config:     config,
		API:        api,
		Snapshot:   snapshotData,
	}
	return b
}

func (b *BIOS) Run() error {
	fmt.Println("Start BIOS process", time.Now())

	if err := b.DispatchInit(); err != nil {
		return fmt.Errorf("failed init hook: %s", err)
	}

	b.PrintAppointedBlockProducers()

	if b.AmIBootNode() {
		if err := b.RunBootNodeStage1(); err != nil {
			return fmt.Errorf("boot node stage1: %s", err)
		}
	} else if b.AmIAppointedBlockProducer() {
		if err := b.RunABPStage1(); err != nil {
			return fmt.Errorf("abp stage1: %s", err)
		}
	} else {
		if err := b.WaitStage1End(); err != nil {
			return fmt.Errorf("waiting stage1: %s", err)
		}
	}

	fmt.Println("Registering my producer account")

	_, err := b.API.SignPushActions(system.NewRegProducer(AN(b.Config.Producer.MyAccount), b.Config.Producer.BlockSigningPublicKey, b.Config.MyParameters))
	if err != nil {
		return fmt.Errorf("regproducer: %s", err)
	}

	fmt.Println("BIOS Sequence Terminated")

	return b.DispatchDone()
}

func (b *BIOS) PrintAppointedBlockProducers() {
	fmt.Println("###############################################################################################")
	fmt.Println("###################################  SHUFFLING RESULTS  #######################################")
	fmt.Println("")

	fmt.Printf("BIOS NODE: %s\n", b.ShuffledProducers[0].String())
	for i := 1; i < 22 && len(b.ShuffledProducers) > i; i++ {
		fmt.Printf("ABP %02d:    %s\n", i, b.ShuffledProducers[i].String())
	}
	fmt.Println("")
	fmt.Println("###############################################################################################")
	fmt.Println("########################################  BOOTING  ############################################")
	fmt.Println("")
	if b.AmIBootNode() {
		fmt.Println("I AM THE BOOT NODE! Let's get the ball rolling.")

	} else if b.AmIAppointedBlockProducer() {
		fmt.Println("I am NOT the BOOT NODE, but I AM ONE of the Appointed Block Producers. Stay tuned and watch the Boot node's media properties.")
	} else {
		fmt.Println("Okay... I'm not part of the Appointed Block Producers, we'll wait and be ready to join")
	}
	fmt.Println("")

	fmt.Println("###############################################################################################")
	fmt.Println("")
}

func (b *BIOS) RunBootNodeStage1() error {
	ephemeralPrivateKey, err := b.GenerateEphemeralPrivKey()
	if err != nil {
		return err
	}

	b.EphemeralPrivateKey = ephemeralPrivateKey

	// b.API.Debug = true

	pubKey := ephemeralPrivateKey.PublicKey().String()
	privKey := ephemeralPrivateKey.String()

	fmt.Println("Generated ephemeral private keys:", pubKey, privKey)

	// Store keys in wallet, to sign `SetCode` and friends..
	if err := b.API.Signer.ImportPrivateKey(privKey); err != nil {
		return fmt.Errorf("ImportWIF: %s", err)
	}

	keys, _ := b.API.Signer.(*eos.KeyBag).AvailableKeys()
	for _, key := range keys {
		fmt.Println("Available key in the KeyBag:", key)
	}

	genesisData := b.GenerateGenesisJSON(pubKey)

	if err = b.DispatchStartBIOSBoot(genesisData, pubKey, privKey); err != nil {
		return fmt.Errorf("dispatch config_ready hook: %s", err)
	}

	fmt.Println(b.API.Signer.AvailableKeys())

	// Run boot sequence

	// TODO: add an action at the end, with `nonce` and a message to indicate the end of the Boot process ?
	// This way, nodes that sync can assume all boot actions are done once that nonce action goes through.
	for _, step := range b.LaunchData.BootSequence {
		fmt.Printf("%s  [%s]\n", step.Label, step.Op)

		acts, err := step.Data.Actions(b)
		if err != nil {
			return fmt.Errorf("getting actions for step %q: %s", step.Op, err)
		}

		if len(acts) != 0 {
			for idx, chunk := range chunkifyActions(acts, 400) { // transfers max out resources higher than ~400
				_, err = b.API.SignPushActions(chunk...)
				if err != nil {
					return fmt.Errorf("SignPushActions for step %q, chunk %d: %s", step.Op, idx, err)
				}
			}
		}
	}

	fmt.Println("Preparing kickstart data")

	kickstartData := &KickstartData{
		BIOSP2PAddress: b.Config.Producer.SecretP2PAddress,
		PublicKeyUsed:  pubKey,
		PrivateKeyUsed: privKey,
		GenesisJSON:    genesisData,
	}
	kd, _ := json.Marshal(kickstartData)
	ksdata := base64.RawStdEncoding.EncodeToString(kd)

	// TODO: encrypt it for those who need it

	fmt.Println("PUBLISH THIS KICKSTART DATA:")
	fmt.Println("")
	fmt.Println(ksdata)
	fmt.Println("")

	if err = b.DispatchPublishKickstartData(ksdata); err != nil {
		return fmt.Errorf("dispatch publish_kickstart_data: %s", err)
	}

	// Call `regproducer` for myself now

	return nil
}

func (b *BIOS) RunABPStage1() error {
	fmt.Println("Waiting on kickstart data from the BIOS Node.")
	fmt.Println("Paste it in here. Finish with a blank line (ENTER)")

	kickstart, err := b.waitOnKickstartData()
	if err != nil {
		return err
	}

	// TODO: Decrypt the Kickstart data
	//   Do extensive validation on the input (tight regexp for address, for private key?)

	if err = b.DispatchConnectAsABP(kickstart, b.MyProducerDefs); err != nil {
		return err
	}

	fmt.Println("###############################################################################################")
	fmt.Println("As an Appointer Block Producer, we're now launching battery of verifications...")

	fmt.Printf("- Verifying the `eosio` system account was properly disabled: ")
	for {
		time.Sleep(1 * time.Second)
		acct, err := b.API.GetAccount(AN("eosio"))
		if err != nil {
			fmt.Printf("e")
			continue
		}

		if len(acct.Permissions) != 2 || acct.Permissions[0].RequiredAuth.Threshold != 0 || acct.Permissions[1].RequiredAuth.Threshold != 0 {
			// FIXME: perhaps check that there are no keys and
			// accounts.. that the account is *really* disabled.  we
			// can check elsewhere though.
			fmt.Printf(".")
			continue
		}

		fmt.Println(" OKAY")
		break
	}

	fmt.Println("Chain sync'd!")

	// TODO: loop operations, check all actions against blocks that you can fetch from here.
	// Do all the checks:
	//  - all Producers are properly setup
	//  - anything fails, SABOTAGE
	// Publish a PGP Signed message with your local IP.. push to properties
	// Dispatch webhook PublishKickstartPublic (with a Kickstart Data object)

	return nil
}

func (b *BIOS) WaitStage1End() error {
	fmt.Println("Waiting for Appointed Block Producers to finish their jobs. Check their social presence!")

	// TODO: check if kickstartData invalid, then either ignore it or destroy the network
	// TODO: rather, loop for kickstar tdatas, until something valid is dropped in..
	kickstart, err := b.waitOnKickstartData()
	if err != nil {
		return err
	}

	if err = b.DispatchConnectAsParticipant(kickstart, b.MyProducerDefs[0]); err != nil {
		return err
	}

	fmt.Println("Not doing any validation, the ABPs have done it")

	return nil
}

func (b *BIOS) waitOnKickstartData() (kickstart KickstartData, err error) {
	// Wait on stdin for kickstart data (will we have some other polling / subscription mechanisms?)
	//    Accept any base64, unpadded, multi-line until we receive a blank line, concat and decode.
	// FIXME: this is a quick hack to just pass the p2p address
	lines, err := ScanLinesUntilBlank()
	if err != nil {
		return
	}

	rawKickstartData, err := base64.RawStdEncoding.DecodeString(strings.Replace(strings.TrimSpace(lines), "\n", "", -1))
	if err != nil {
		return kickstart, fmt.Errorf("kickstart base64 decode: %s", err)
	}

	err = json.Unmarshal(rawKickstartData, &kickstart)
	if err != nil {
		return kickstart, fmt.Errorf("unmarshal kickstart data: %s", err)
	}

	privKey, err := ecc.NewPrivateKey(kickstart.PrivateKeyUsed)
	if err != nil {
		return kickstart, fmt.Errorf("unable to load private key %q: %s", kickstart.PrivateKeyUsed, err)
	}

	b.EphemeralPrivateKey = privKey

	// TODO: check if the privKey corresponds to the public key sent, if not, we should
	// drop that kickstart data.. and listen to another one..

	return
}

func (b *BIOS) GenerateEphemeralPrivKey() (*ecc.PrivateKey, error) {
	return ecc.NewRandomPrivateKey()
}

func (b *BIOS) GenerateGenesisJSON(pubKey string) string {
	// known not to fail
	cnt, _ := json.Marshal(&GenesisJSON{
		InitialTimestamp: b.ShuffleBlock.Time.UTC().Format("2006-01-02T15:04:05"),
		InitialKey:       pubKey,
		InitialChainID:   hex.EncodeToString(b.API.ChainID),
	})
	return string(cnt)
}

func (b *BIOS) ShuffleProducers(btcMerkleRoot []byte, blockTime time.Time) error {
	// we'll shuffle later :)
	if b.Config.Debug.NoShuffle {
		fmt.Println("DEBUG: Skipping shuffle, using order in launch.yaml")
		b.ShuffledProducers = b.LaunchData.Producers
		b.ShuffleBlock.Time = time.Now().UTC()
		b.ShuffleBlock.MerkleRoot = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	} else {
		fmt.Println("Shuffling producers listed in the launch file [NOT IMPLEMENTED]")
		// TODO: write the algorithm...
		b.ShuffledProducers = b.LaunchData.Producers
		b.ShuffleBlock.Time = blockTime
		b.ShuffleBlock.MerkleRoot = btcMerkleRoot
	}

	// We'll multiply the other producers as to have a full schedule
	if numProds := len(b.ShuffledProducers); numProds < 22 {
		cloneCount := numProds - 1
		count := 0
		for {
			if len(b.ShuffledProducers) == 22 {
				break
			}

			fromProd := b.ShuffledProducers[1+count%cloneCount]
			count++

			clonedProd := &ProducerDef{
				AccountName:                  accountVariation(fromProd.AccountName, count),
				Authority:                    fromProd.Authority,
				InitialBlockSigningPublicKey: fromProd.InitialBlockSigningPublicKey,
				KeybaseUser:                  fromProd.KeybaseUser,
				PGPPublicKey:                 fromProd.PGPPublicKey,
				OrganizationName:             fromProd.OrganizationName + fmt.Sprintf(" - clone %d", count),
				Timezone:                     fromProd.Timezone,
				URLs:                         fromProd.URLs,
				clonedFrom:                   fromProd.AccountName,
			}
			b.ShuffledProducers = append(b.ShuffledProducers, clonedProd)
		}
	}

	return nil
}

func (b *BIOS) IsBootNode(account string) bool {
	return string(b.ShuffledProducers[0].AccountName) == account
}

func (b *BIOS) AmIBootNode() bool {
	return b.IsBootNode(b.Config.Producer.MyAccount)
}

func (b *BIOS) IsAppointedBlockProducer(account string) bool {
	for i := 1; i < 22 && len(b.ShuffledProducers) > i; i++ {
		if string(b.ShuffledProducers[i].AccountName) == account {
			return true
		}
	}
	return false
}

func (b *BIOS) AmIAppointedBlockProducer() bool {
	return b.IsAppointedBlockProducer(b.Config.Producer.MyAccount)
}

func (b *BIOS) MyProducerDef() (*ProducerDef, error) {
	for _, prod := range b.LaunchData.Producers {
		if b.Config.Producer.MyAccount == string(prod.AccountName) {
			return prod, nil
		}
	}
	return nil, fmt.Errorf("no local producer config found (make sure producer.my_account in your config matches an entry in the launch file)")
}

// MyProducerDefs will provide more than one producer def ONLY when
// your launch files contains LESS than 21 potential appointed block
// producers.  This way, you can have your nodes respond to many
// account names and have the network function. Your producer will
// simply produce more blocks, under different names.
func (b *BIOS) setMyProducerDefs() error {
	myProd, err := b.MyProducerDef()
	if err != nil {
		return err
	}

	out := []*ProducerDef{myProd}

	for _, prod := range b.ShuffledProducers {
		if prod.clonedFrom == myProd.AccountName {
			out = append(out, prod)
		}
	}

	b.MyProducerDefs = out

	return nil
}

func chunkifyActions(actions []*eos.Action, chunkSize int) (out [][]*eos.Action) {
	currentChunk := []*eos.Action{}
	for _, act := range actions {
		if len(currentChunk) > chunkSize {
			out = append(out, currentChunk)
			currentChunk = []*eos.Action{}
		}
		currentChunk = append(currentChunk, act)
	}
	if len(currentChunk) > 0 {
		out = append(out, currentChunk)
	}
	return
}

func accountVariation(name eos.AccountName, variation int) eos.AccountName {
	if len(name) > 10 {
		name = AN(string(name)[:10])
	}
	return AN(string(name) + "." + string([]byte{'a' + byte(variation-1)}))
}
