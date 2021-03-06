
//此源码被清华学神尹成大魔王专业翻译分析并修改
//尹成QQ77025077
//尹成微信18510341407
//尹成所在QQ群721929980
//尹成邮箱 yinc13@mails.tsinghua.edu.cn
//尹成毕业于清华大学,微软区块链领域全球最有价值专家
//https://mvp.microsoft.com/zh-cn/PublicProfile/4033620
/*
版权所有IBM公司。保留所有权利。

SPDX许可证标识符：Apache-2.0
**/


package multichannel

import (
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric/common/crypto"
	"github.com/hyperledger/fabric/common/flogging"
	"github.com/hyperledger/fabric/common/ledger/blockledger"
	ramledger "github.com/hyperledger/fabric/common/ledger/blockledger/ram"
	"github.com/hyperledger/fabric/common/metrics/disabled"
	mockchannelconfig "github.com/hyperledger/fabric/common/mocks/config"
	mockcrypto "github.com/hyperledger/fabric/common/mocks/crypto"
	mmsp "github.com/hyperledger/fabric/common/mocks/msp"
	mockpolicies "github.com/hyperledger/fabric/common/mocks/policies"
	"github.com/hyperledger/fabric/common/tools/configtxgen/configtxgentest"
	"github.com/hyperledger/fabric/common/tools/configtxgen/encoder"
	genesisconfig "github.com/hyperledger/fabric/common/tools/configtxgen/localconfig"
	"github.com/hyperledger/fabric/msp"
	"github.com/hyperledger/fabric/orderer/common/blockcutter"
	"github.com/hyperledger/fabric/orderer/consensus"
	cb "github.com/hyperledger/fabric/protos/common"
	ab "github.com/hyperledger/fabric/protos/orderer"
	"github.com/hyperledger/fabric/protos/utils"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

var conf *genesisconfig.Profile
var genesisBlock *cb.Block
var mockSigningIdentity msp.SigningIdentity

const NoConsortiumChain = "no-consortium-chain"

func init() {
	flogging.ActivateSpec("orderer.commmon.multichannel=DEBUG")
	mockSigningIdentity, _ = mmsp.NewNoopMsp().GetDefaultSigningIdentity()

	conf = configtxgentest.Load(genesisconfig.SampleInsecureSoloProfile)
	genesisBlock = encoder.New(conf).GenesisBlock()
}

func mockCrypto() crypto.LocalSigner {
	return mockcrypto.FakeLocalSigner
}

func NewRAMLedgerAndFactory(maxSize int) (blockledger.Factory, blockledger.ReadWriter) {
	rlf := ramledger.New(maxSize)
	rl, err := rlf.GetOrCreate(genesisconfig.TestChainID)
	if err != nil {
		panic(err)
	}
	err = rl.Append(genesisBlock)
	if err != nil {
		panic(err)
	}
	return rlf, rl
}

func NewRAMLedger(maxSize int) blockledger.ReadWriter {
	_, rl := NewRAMLedgerAndFactory(maxSize)
	return rl
}

//测试包含3个配置事务和其他正常事务的正常链，以确保返回正确的事务。
func TestGetConfigTx(t *testing.T) {
	rl := NewRAMLedger(10)
	for i := 0; i < 5; i++ {
		rl.Append(blockledger.CreateNextBlock(rl, []*cb.Envelope{makeNormalTx(genesisconfig.TestChainID, i)}))
	}
	rl.Append(blockledger.CreateNextBlock(rl, []*cb.Envelope{makeConfigTx(genesisconfig.TestChainID, 5)}))
	ctx := makeConfigTx(genesisconfig.TestChainID, 6)
	rl.Append(blockledger.CreateNextBlock(rl, []*cb.Envelope{ctx}))

	block := blockledger.CreateNextBlock(rl, []*cb.Envelope{makeNormalTx(genesisconfig.TestChainID, 7)})
	block.Metadata.Metadata[cb.BlockMetadataIndex_LAST_CONFIG] = utils.MarshalOrPanic(&cb.Metadata{Value: utils.MarshalOrPanic(&cb.LastConfig{Index: 7})})
	rl.Append(block)

	pctx := getConfigTx(rl)
	assert.True(t, proto.Equal(pctx, ctx), "Did not select most recent config transaction")
}

//测试一个包含多个事务与config txs混合的块的链，以及一个不是config tx的单个tx，none作为config块计数，因此nil应返回
func TestGetConfigTxFailure(t *testing.T) {
	rl := NewRAMLedger(10)
	for i := 0; i < 10; i++ {
		rl.Append(blockledger.CreateNextBlock(rl, []*cb.Envelope{
			makeNormalTx(genesisconfig.TestChainID, i),
			makeConfigTx(genesisconfig.TestChainID, i),
		}))
	}
	rl.Append(blockledger.CreateNextBlock(rl, []*cb.Envelope{makeNormalTx(genesisconfig.TestChainID, 11)}))
	assert.Panics(t, func() { getConfigTx(rl) }, "Should have panicked because there was no config tx")

	block := blockledger.CreateNextBlock(rl, []*cb.Envelope{makeNormalTx(genesisconfig.TestChainID, 12)})
	block.Metadata.Metadata[cb.BlockMetadataIndex_LAST_CONFIG] = []byte("bad metadata")
	assert.Panics(t, func() { getConfigTx(rl) }, "Should have panicked because of bad last config metadata")
}

//此测试检查以确保订购方在找不到系统通道时拒绝上来。
func TestNoSystemChain(t *testing.T) {
	lf := ramledger.New(10)

	consenters := make(map[string]consensus.Consenter)
	consenters[conf.Orderer.OrdererType] = &mockConsenter{}

	assert.Panics(t, func() {
		NewRegistrar(lf, mockCrypto(), &disabled.Provider{}).Initialize(consenters)
	}, "Should have panicked when starting without a system chain")
}

//此测试检查以确保如果存在多个系统通道，订购方拒绝出现
func TestMultiSystemChannel(t *testing.T) {
	lf := ramledger.New(10)

	for _, id := range []string{"foo", "bar"} {
		rl, err := lf.GetOrCreate(id)
		assert.NoError(t, err)

		err = rl.Append(encoder.New(conf).GenesisBlockForChannel(id))
		assert.NoError(t, err)
	}

	consenters := make(map[string]consensus.Consenter)
	consenters[conf.Orderer.OrdererType] = &mockConsenter{}

	assert.Panics(t, func() {
		NewRegistrar(lf, mockCrypto(), &disabled.Provider{}).Initialize(consenters)
	}, "Two system channels should have caused panic")
}

//这个测试从本质上来说是整个系统的启动，并且最终是main.go将要复制的内容。
func TestManagerImpl(t *testing.T) {
	lf, rl := NewRAMLedgerAndFactory(10)

	consenters := make(map[string]consensus.Consenter)
	consenters[conf.Orderer.OrdererType] = &mockConsenter{}

	manager := NewRegistrar(lf, mockCrypto(), &disabled.Provider{})
	manager.Initialize(consenters)

	chainSupport := manager.GetChain("Fake")
	assert.Nilf(t, chainSupport, "Should not have found a chain that was not created")

	chainSupport = manager.GetChain(genesisconfig.TestChainID)
	assert.NotNilf(t, chainSupport, "Should have gotten chain which was initialized by ramledger")

	messages := make([]*cb.Envelope, conf.Orderer.BatchSize.MaxMessageCount)
	for i := 0; i < int(conf.Orderer.BatchSize.MaxMessageCount); i++ {
		messages[i] = makeNormalTx(genesisconfig.TestChainID, i)
	}

	for _, message := range messages {
		chainSupport.Order(message, 0)
	}

	it, _ := rl.Iterator(&ab.SeekPosition{Type: &ab.SeekPosition_Specified{Specified: &ab.SeekSpecified{Number: 1}}})
	defer it.Close()
	block, status := it.Next()
	assert.Equal(t, cb.Status_SUCCESS, status, "Could not retrieve block")
	for i := 0; i < int(conf.Orderer.BatchSize.MaxMessageCount); i++ {
		assert.True(t, proto.Equal(messages[i], utils.ExtractEnvelopeOrPanic(block, i)), "Block contents wrong at index %d", i)
	}
}

//这个测试带来了整个系统，以及模拟同意者，包括广播公司等，并创建了一个新的链。
func TestNewChain(t *testing.T) {
	expectedLastConfigBlockNumber := uint64(0)
	expectedLastConfigSeq := uint64(1)
	newChainID := "test-new-chain"

	lf, rl := NewRAMLedgerAndFactory(10)

	consenters := make(map[string]consensus.Consenter)
	consenters[conf.Orderer.OrdererType] = &mockConsenter{}

	manager := NewRegistrar(lf, mockCrypto(), &disabled.Provider{})
	manager.Initialize(consenters)
	orglessChannelConf := configtxgentest.Load(genesisconfig.SampleSingleMSPChannelProfile)
	orglessChannelConf.Application.Organizations = nil
	envConfigUpdate, err := encoder.MakeChannelCreationTransaction(newChainID, mockCrypto(), orglessChannelConf)
	assert.NoError(t, err, "Constructing chain creation tx")

	res, err := manager.NewChannelConfig(envConfigUpdate)
	assert.NoError(t, err, "Constructing initial channel config")

	configEnv, err := res.ConfigtxValidator().ProposeConfigUpdate(envConfigUpdate)
	assert.NoError(t, err, "Proposing initial update")
	assert.Equal(t, expectedLastConfigSeq, configEnv.GetConfig().Sequence, "Sequence of config envelope for new channel should always be set to %d", expectedLastConfigSeq)

	ingressTx, err := utils.CreateSignedEnvelope(cb.HeaderType_CONFIG, newChainID, mockCrypto(), configEnv, msgVersion, epoch)
	assert.NoError(t, err, "Creating ingresstx")

	wrapped := wrapConfigTx(ingressTx)

	chainSupport := manager.GetChain(manager.SystemChannelID())
	assert.NotNilf(t, chainSupport, "Could not find system channel")

	chainSupport.Configure(wrapped, 0)
	func() {
		it, _ := rl.Iterator(&ab.SeekPosition{Type: &ab.SeekPosition_Specified{Specified: &ab.SeekSpecified{Number: 1}}})
		defer it.Close()
		block, status := it.Next()
		if status != cb.Status_SUCCESS {
			t.Fatalf("Could not retrieve block")
		}
		if len(block.Data.Data) != 1 {
			t.Fatalf("Should have had only one message in the orderer transaction block")
		}

		assert.True(t, proto.Equal(wrapped, utils.UnmarshalEnvelopeOrPanic(block.Data.Data[0])), "Orderer config block contains wrong transaction")
	}()

	chainSupport = manager.GetChain(newChainID)
	if chainSupport == nil {
		t.Fatalf("Should have gotten new chain which was created")
	}

	messages := make([]*cb.Envelope, conf.Orderer.BatchSize.MaxMessageCount)
	for i := 0; i < int(conf.Orderer.BatchSize.MaxMessageCount); i++ {
		messages[i] = makeNormalTx(newChainID, i)
	}

	for _, message := range messages {
		chainSupport.Order(message, 0)
	}

	it, _ := chainSupport.Reader().Iterator(&ab.SeekPosition{Type: &ab.SeekPosition_Specified{Specified: &ab.SeekSpecified{Number: 0}}})
	defer it.Close()
	block, status := it.Next()
	if status != cb.Status_SUCCESS {
		t.Fatalf("Could not retrieve new chain genesis block")
	}
	testLastConfigBlockNumber(t, block, expectedLastConfigBlockNumber)
	if len(block.Data.Data) != 1 {
		t.Fatalf("Should have had only one message in the new genesis block")
	}

	assert.True(t, proto.Equal(ingressTx, utils.UnmarshalEnvelopeOrPanic(block.Data.Data[0])), "Genesis block contains wrong transaction")

	block, status = it.Next()
	if status != cb.Status_SUCCESS {
		t.Fatalf("Could not retrieve block on new chain")
	}
	testLastConfigBlockNumber(t, block, expectedLastConfigBlockNumber)
	for i := 0; i < int(conf.Orderer.BatchSize.MaxMessageCount); i++ {
		if !proto.Equal(utils.ExtractEnvelopeOrPanic(block, i), messages[i]) {
			t.Errorf("Block contents wrong at index %d in new chain", i)
		}
	}

	rcs := newChainSupport(manager, chainSupport.ledgerResources, consenters, mockCrypto(), blockcutter.NewMetrics(&disabled.Provider{}))
	assert.Equal(t, expectedLastConfigSeq, rcs.lastConfigSeq, "On restart, incorrect lastConfigSeq")
}

func testLastConfigBlockNumber(t *testing.T, block *cb.Block, expectedBlockNumber uint64) {
	metadataItem := &cb.Metadata{}
	err := proto.Unmarshal(block.Metadata.Metadata[cb.BlockMetadataIndex_LAST_CONFIG], metadataItem)
	assert.NoError(t, err, "Block should carry LAST_CONFIG metadata item")
	lastConfig := &cb.LastConfig{}
	err = proto.Unmarshal(metadataItem.Value, lastConfig)
	assert.NoError(t, err, "LAST_CONFIG metadata item should carry last config value")
	assert.Equal(t, expectedBlockNumber, lastConfig.Index, "LAST_CONFIG value should point to last config block")
}

func TestResourcesCheck(t *testing.T) {
	t.Run("GoodResources", func(t *testing.T) {
		err := checkResources(&mockchannelconfig.Resources{
			PolicyManagerVal: &mockpolicies.Manager{},
			OrdererConfigVal: &mockchannelconfig.Orderer{
				CapabilitiesVal: &mockchannelconfig.OrdererCapabilities{},
			},
			ChannelConfigVal: &mockchannelconfig.Channel{
				CapabilitiesVal: &mockchannelconfig.ChannelCapabilities{},
			},
		})

		assert.NoError(t, err)
	})

	t.Run("MissingOrdererConfigPanic", func(t *testing.T) {
		err := checkResources(&mockchannelconfig.Resources{
			PolicyManagerVal: &mockpolicies.Manager{},
		})

		assert.Error(t, err)
		assert.Regexp(t, "config does not contain orderer config", err.Error())
	})

	t.Run("MissingOrdererCapability", func(t *testing.T) {
		err := checkResources(&mockchannelconfig.Resources{
			PolicyManagerVal: &mockpolicies.Manager{},
			OrdererConfigVal: &mockchannelconfig.Orderer{
				CapabilitiesVal: &mockchannelconfig.OrdererCapabilities{
					SupportedErr: errors.New("An error"),
				},
			},
		})

		assert.Error(t, err)
		assert.Regexp(t, "config requires unsupported orderer capabilities:", err.Error())
	})

	t.Run("MissingChannelCapability", func(t *testing.T) {
		err := checkResources(&mockchannelconfig.Resources{
			PolicyManagerVal: &mockpolicies.Manager{},
			OrdererConfigVal: &mockchannelconfig.Orderer{
				CapabilitiesVal: &mockchannelconfig.OrdererCapabilities{},
			},
			ChannelConfigVal: &mockchannelconfig.Channel{
				CapabilitiesVal: &mockchannelconfig.ChannelCapabilities{
					SupportedErr: errors.New("An error"),
				},
			},
		})

		assert.Error(t, err)
		assert.Regexp(t, "config requires unsupported channel capabilities:", err.Error())
	})

	t.Run("MissingOrdererConfigPanic", func(t *testing.T) {
		assert.Panics(t, func() {
			checkResourcesOrPanic(&mockchannelconfig.Resources{
				PolicyManagerVal: &mockpolicies.Manager{},
			})
		})
	})
}

//注册器的BroadcastChannelSupport实现应拒绝不应直接处理的消息类型。
func TestBroadcastChannelSupportRejection(t *testing.T) {
	ledgerFactory, _ := NewRAMLedgerAndFactory(10)
	mockConsenters := map[string]consensus.Consenter{conf.Orderer.OrdererType: &mockConsenter{}}
	registrar := NewRegistrar(ledgerFactory, mockCrypto(), &disabled.Provider{})
	registrar.Initialize(mockConsenters)
	randomValue := 1
	configTx := makeConfigTx(genesisconfig.TestChainID, randomValue)
	_, _, _, err := registrar.BroadcastChannelSupport(configTx)
	assert.Error(t, err, "Messages of type HeaderType_CONFIG should return an error.")
}
