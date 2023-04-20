package cosmos

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	cosmosclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/types"
	bankTypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	chanTypes "github.com/cosmos/ibc-go/v3/modules/core/04-channel/types"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/strangelove-ventures/interchaintest/v3/chain/internal/tendermint"
	"github.com/strangelove-ventures/interchaintest/v3/ibc"
	"github.com/strangelove-ventures/interchaintest/v3/internal/blockdb"
	rpcclient "github.com/tendermint/tendermint/rpc/client"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type CosmosExternalChain struct {
	TendermintClient rpcclient.Client
	CosmosClient     cosmosclient.Context
	cfg              ibc.ChainConfig
	log              *zap.Logger
	FullNode         *ChainNode
}

func NewCosmosExternalChain(log *zap.Logger, cfg ibc.ChainConfig) (*CosmosExternalChain, error) {
	tmClient, err := NewTendermintClient(cfg.Address)
	if err != nil {
		return nil, err
	}
	if cfg.EncodingConfig == nil {
		ec := DefaultEncoding()
		cfg.EncodingConfig = &ec
	}
	cosmosClient := NewCosmosClient(cfg.ChainID, tmClient, *cfg.EncodingConfig)
	if err != nil {
		return nil, err
	}
	return &CosmosExternalChain{
		TendermintClient: tmClient,
		CosmosClient:     cosmosClient,
		log:              log,
		cfg:              cfg,
	}, nil
}

// Implements Chain interface
// Config fetches the chain configuration.
func (c *CosmosExternalChain) Config() ibc.ChainConfig {
	return c.cfg
}

// Implements Chain interface
func (c *CosmosExternalChain) Initialize(ctx context.Context, testName string, cli *client.Client, networkID string) error {
	for _, image := range c.cfg.Images {
		rc, err := cli.ImagePull(
			ctx,
			image.Repository+":"+image.Version,
			dockertypes.ImagePullOptions{},
		)
		if err != nil {
			c.log.Error("Failed to pull image",
				zap.Error(err),
				zap.String("repository", image.Repository),
				zap.String("tag", image.Version),
			)
		} else {
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
		}
	}
	fn, err := NewCosmosChainNode(c.log, c, ctx, testName, cli, networkID, c.cfg.Images[0], false)
	if err != nil {
		return err
	}
	fn.TendermintClient = c.TendermintClient
	c.FullNode = fn
	return nil
}

func (c *CosmosExternalChain) getFullNode() *ChainNode {
	return c.FullNode
}

// Implements Chain interface
func (c *CosmosExternalChain) Start(testName string, ctx context.Context, additionalGenesisWallets ...ibc.WalletAmount) error {
	// Since this is an external chain, we don't want to do anything here. Assumes chain is already producing blocks.
	return nil
}

// FindTxs implements blockdb.BlockSaver.
func (c *CosmosExternalChain) FindTxs(ctx context.Context, height uint64) ([]blockdb.Tx, error) {
	return c.FullNode.FindTxs(ctx, height)
}

// Implements Chain interface
// Exec runs an arbitrary command using Chain's docker environment.
// Whether the invoked command is run in a one-off container or execing into an already running container
// is up to the chain implementation.
//
// "env" are environment variables in the format "MY_ENV_VAR=value"
func (c *CosmosExternalChain) Exec(ctx context.Context, cmd []string, env []string) (stdout, stderr []byte, err error) {
	return c.FullNode.Exec(ctx, cmd, env)
}

// Implements Chain interface
// ExportState exports the chain state at specific height.
func (c *CosmosExternalChain) ExportState(ctx context.Context, height int64) (string, error) {
	return c.FullNode.ExportState(ctx, height)
}

// Implements Chain interface
func (c *CosmosExternalChain) GetRPCAddress() string {
	return fmt.Sprintf("http://%s:26657", c.FullNode.HostName())
}

// Implements Chain interface
func (c *CosmosExternalChain) GetGRPCAddress() string {
	return fmt.Sprintf("%s:9090", c.FullNode.HostName())
}

// Implements Chain interface
// GetHostRPCAddress returns the address of the RPC server accessible by the host.
// This will not return a valid address until the chain has been started.
func (c *CosmosExternalChain) GetHostRPCAddress() string {
	return "http://" + c.FullNode.hostRPCPort
}

// Implements Chain interface
// GetHostGRPCAddress returns the address of the gRPC server accessible by the host.
// This will not return a valid address until the chain has been started.
func (c *CosmosExternalChain) GetHostGRPCAddress() string {
	return c.FullNode.hostGRPCPort
}

// Implements Chain interface
// HomeDir is the home directory of a node running in a docker container. Therefore, this maps to
// the container's filesystem (not the host).
func (c *CosmosExternalChain) HomeDir() string {
	return c.FullNode.HomeDir()
}

// Implements Chain interface
// CreateKey creates a test key in the "user" node (either the first FullNode or the first validator if no fullnodes).
func (c *CosmosExternalChain) CreateKey(ctx context.Context, keyName string) error {
	return c.FullNode.CreateKey(ctx, keyName)
}

// Implements Chain interface
// RecoverKey recovers an existing user from a given mnemonic.
func (c *CosmosExternalChain) RecoverKey(ctx context.Context, name, mnemonic string) error {
	return c.FullNode.RecoverKey(ctx, name, mnemonic)
}

// Implements Chain interface
// GetAddress fetches the bech32 address for a test key on the "user" node (either the first FullNode or the first validator if no fullnodes).
func (c *CosmosExternalChain) GetAddress(ctx context.Context, keyName string) ([]byte, error) {
	b32Addr, err := c.FullNode.KeyBech32(ctx, keyName, "")
	if err != nil {
		return nil, err
	}

	return types.GetFromBech32(b32Addr, c.Config().Bech32Prefix)
}

// Implements Chain interface
// Implements Chain interface
func (c *CosmosExternalChain) SendFunds(ctx context.Context, keyName string, amount ibc.WalletAmount) error {
	return c.FullNode.SendFunds(ctx, keyName, amount)
}

// Implements Chain interface
// Implements Chain interface
func (c *CosmosExternalChain) SendIBCTransfer(ctx context.Context, channelID, keyName string, amount ibc.WalletAmount, options ibc.TransferOptions) (tx ibc.Tx, _ error) {
	txHash, err := c.FullNode.SendIBCTransfer(ctx, channelID, keyName, amount, options)
	if err != nil {
		return tx, fmt.Errorf("send ibc transfer: %w", err)
	}
	txResp, err := c.FullNode.Transaction(txHash)
	if err != nil {
		return tx, fmt.Errorf("failed to get transaction %s: %w", txHash, err)
	}
	tx.Height = uint64(txResp.Height)
	tx.TxHash = txHash
	// In cosmos, user is charged for entire gas requested, not the actual gas used.
	tx.GasSpent = txResp.GasWanted

	const evType = "send_packet"
	events := txResp.Events

	var (
		seq, _           = tendermint.AttributeValue(events, evType, "packet_sequence")
		srcPort, _       = tendermint.AttributeValue(events, evType, "packet_src_port")
		srcChan, _       = tendermint.AttributeValue(events, evType, "packet_src_channel")
		dstPort, _       = tendermint.AttributeValue(events, evType, "packet_dst_port")
		dstChan, _       = tendermint.AttributeValue(events, evType, "packet_dst_channel")
		timeoutHeight, _ = tendermint.AttributeValue(events, evType, "packet_timeout_height")
		timeoutTs, _     = tendermint.AttributeValue(events, evType, "packet_timeout_timestamp")
		data, _          = tendermint.AttributeValue(events, evType, "packet_data")
	)
	tx.Packet.SourcePort = srcPort
	tx.Packet.SourceChannel = srcChan
	tx.Packet.DestPort = dstPort
	tx.Packet.DestChannel = dstChan
	tx.Packet.TimeoutHeight = timeoutHeight
	tx.Packet.Data = []byte(data)

	seqNum, err := strconv.Atoi(seq)
	if err != nil {
		return tx, fmt.Errorf("invalid packet sequence from events %s: %w", seq, err)
	}
	tx.Packet.Sequence = uint64(seqNum)

	timeoutNano, err := strconv.ParseUint(timeoutTs, 10, 64)
	if err != nil {
		return tx, fmt.Errorf("invalid packet timestamp timeout %s: %w", timeoutTs, err)
	}
	tx.Packet.TimeoutTimestamp = ibc.Nanoseconds(timeoutNano)

	return tx, nil
}

// UpgradeProposal submits a software-upgrade governance proposal to the chain.
func (c *CosmosExternalChain) UpgradeProposal(ctx context.Context, keyName string, prop SoftwareUpgradeProposal) (tx TxProposal, _ error) {
	txHash, err := c.getFullNode().UpgradeProposal(ctx, keyName, prop)
	if err != nil {
		return tx, fmt.Errorf("failed to submit upgrade proposal: %w", err)
	}
	return c.txProposal(txHash)
}

// InstantiateContract takes a code id for a smart contract and initialization message and returns the instantiated contract address.
func (c *CosmosExternalChain) InstantiateContract(ctx context.Context, keyName string, codeID string, initMessage string, needsNoAdminFlag bool) (string, error) {
	return c.FullNode.InstantiateContract(ctx, keyName, codeID, initMessage, needsNoAdminFlag)
}

// Implements Chain interface
func (c *CosmosExternalChain) ExecuteContract(ctx context.Context, keyName string, contractAddress string, message string) (txHash string, err error) {
	return c.FullNode.ExecuteContract(ctx, keyName, contractAddress, message)
}

// Implements Chain interface
func (c *CosmosExternalChain) DumpContractState(ctx context.Context, contractAddress string, height int64) (*DumpContractStateResponse, error) {
	return c.FullNode.DumpContractState(ctx, contractAddress, height)
}

func (c *CosmosExternalChain) txProposal(txHash string) (tx TxProposal, _ error) {
	txResp, err := c.getFullNode().Transaction(txHash)
	if err != nil {
		return tx, fmt.Errorf("failed to get transaction %s: %w", txHash, err)
	}
	tx.Height = uint64(txResp.Height)
	tx.TxHash = txHash
	// In cosmos, user is charged for entire gas requested, not the actual gas used.
	tx.GasSpent = txResp.GasWanted
	events := txResp.Events

	tx.DepositAmount, _ = tendermint.AttributeValue(events, "proposal_deposit", "amount")

	evtSubmitProp := "submit_proposal"
	tx.ProposalID, _ = tendermint.AttributeValue(events, evtSubmitProp, "proposal_id")
	tx.ProposalType, _ = tendermint.AttributeValue(events, evtSubmitProp, "proposal_type")

	return tx, nil
}

// Implements Chain interface
func (c *CosmosExternalChain) GetBalance(ctx context.Context, address string, denom string) (int64, error) {
	params := &bankTypes.QueryBalanceRequest{Address: address, Denom: denom}
	grpcAddress := c.FullNode.hostGRPCPort
	conn, err := grpc.Dial(grpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	queryClient := bankTypes.NewQueryClient(conn)
	res, err := queryClient.Balance(ctx, params)

	if err != nil {
		return 0, err
	}

	return res.Balance.Amount.Int64(), nil
}

func (c *CosmosExternalChain) GetGasFeesInNativeDenom(gasPaid int64) int64 {
	gasPrice, _ := strconv.ParseFloat(strings.Replace(c.cfg.GasPrices, c.cfg.Denom, "", 1), 64)
	fees := float64(gasPaid) * gasPrice
	return int64(fees)
}

// Height returns the current block height or an error if unable to get current height.
func (c *CosmosExternalChain) Height(ctx context.Context) (uint64, error) {
	res, err := c.TendermintClient.Status(ctx)
	if err != nil {
		return 0, fmt.Errorf("tendermint rpc client status: %w", err)
	}
	height := res.SyncInfo.LatestBlockHeight
	return uint64(height), nil
}

// Acknowledgements implements ibc.Chain, returning all acknowledgments in block at height
func (c *CosmosExternalChain) Acknowledgements(ctx context.Context, height uint64) ([]ibc.PacketAcknowledgement, error) {
	var acks []*chanTypes.MsgAcknowledgement
	err := rangeBlockMessages(ctx, c.cfg.EncodingConfig.InterfaceRegistry, c.TendermintClient, height, func(msg types.Msg) bool {
		found, ok := msg.(*chanTypes.MsgAcknowledgement)
		if ok {
			acks = append(acks, found)
		}
		return false
	})
	if err != nil {
		return nil, fmt.Errorf("find acknowledgements at height %d: %w", height, err)
	}
	ibcAcks := make([]ibc.PacketAcknowledgement, len(acks))
	for i, ack := range acks {
		ack := ack
		ibcAcks[i] = ibc.PacketAcknowledgement{
			Acknowledgement: ack.Acknowledgement,
			Packet: ibc.Packet{
				Sequence:         ack.Packet.Sequence,
				SourcePort:       ack.Packet.SourcePort,
				SourceChannel:    ack.Packet.SourceChannel,
				DestPort:         ack.Packet.DestinationPort,
				DestChannel:      ack.Packet.DestinationChannel,
				Data:             ack.Packet.Data,
				TimeoutHeight:    ack.Packet.TimeoutHeight.String(),
				TimeoutTimestamp: ibc.Nanoseconds(ack.Packet.TimeoutTimestamp),
			},
		}
	}
	return ibcAcks, nil
}

// Timeouts implements ibc.Chain, returning all timeouts in block at height
func (c *CosmosExternalChain) Timeouts(ctx context.Context, height uint64) ([]ibc.PacketTimeout, error) {
	var timeouts []*chanTypes.MsgTimeout
	err := rangeBlockMessages(ctx, c.cfg.EncodingConfig.InterfaceRegistry, c.TendermintClient, height, func(msg types.Msg) bool {
		found, ok := msg.(*chanTypes.MsgTimeout)
		if ok {
			timeouts = append(timeouts, found)
		}
		return false
	})
	if err != nil {
		return nil, fmt.Errorf("find timeouts at height %d: %w", height, err)
	}
	ibcTimeouts := make([]ibc.PacketTimeout, len(timeouts))
	for i, ack := range timeouts {
		ack := ack
		ibcTimeouts[i] = ibc.PacketTimeout{
			Packet: ibc.Packet{
				Sequence:         ack.Packet.Sequence,
				SourcePort:       ack.Packet.SourcePort,
				SourceChannel:    ack.Packet.SourceChannel,
				DestPort:         ack.Packet.DestinationPort,
				DestChannel:      ack.Packet.DestinationChannel,
				Data:             ack.Packet.Data,
				TimeoutHeight:    ack.Packet.TimeoutHeight.String(),
				TimeoutTimestamp: ibc.Nanoseconds(ack.Packet.TimeoutTimestamp),
			},
		}
	}
	return ibcTimeouts, nil
}
