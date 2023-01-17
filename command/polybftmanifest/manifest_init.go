package polybftmanifest

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path"
	"strings"

	"github.com/0xPolygon/polygon-edge/command"
	"github.com/0xPolygon/polygon-edge/command/genesis"
	"github.com/0xPolygon/polygon-edge/consensus/polybft"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/spf13/cobra"
)

const (
	manifestPathFlag      = "path"
	premineValidatorsFlag = "premine-validators"
	stakeFlag             = "stake"
	validatorsFlag        = "validators"
	validatorsPathFlag    = "validators-path"
	validatorsPrefixFlag  = "validators-prefix"

	defaultValidatorPrefixPath = "test-chain-"
	defaultManifestPath        = "./manifest.json"

	nodeIDLength       = 53
	ecdsaAddressLength = 42
	blsKeyLength       = 2
)

var (
	params = &manifestInitParams{}
)

func GetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "manifest",
		Short:   "Initializes manifest file. It is applicable only to polybft consensus protocol.",
		PreRunE: runPreRun,
		Run:     runCommand,
	}

	setFlags(cmd)

	return cmd
}

func runPreRun(_ *cobra.Command, _ []string) error {
	return params.validateFlags()
}

func setFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(
		&params.manifestPath,
		manifestPathFlag,
		defaultManifestPath,
		"the file path where manifest file is going to be stored",
	)

	cmd.Flags().StringVar(
		&params.validatorsPath,
		validatorsPathFlag,
		"./",
		"root path containing polybft validator keys",
	)

	cmd.Flags().StringVar(
		&params.validatorsPrefixPath,
		validatorsPrefixFlag,
		defaultValidatorPrefixPath,
		"folder prefix names for polybft validator keys",
	)

	cmd.Flags().StringArrayVar(
		&params.validators,
		validatorsFlag,
		[]string{},
		"validators defined by user (format: <node id>:<ECDSA address>:<public BLS key>)",
	)

	cmd.Flags().StringVar(
		&params.premineValidators,
		premineValidatorsFlag,
		command.DefaultPremineBalance,
		"the amount which will be pre-mined to all the validators",
	)

	cmd.Flags().StringVar(
		&params.stakeRaw,
		stakeFlag,
		"",
		"the amount which will be staked by all the validators",
	)

	cmd.MarkFlagsMutuallyExclusive(validatorsFlag, validatorsPathFlag)
	cmd.MarkFlagsMutuallyExclusive(validatorsFlag, validatorsPrefixFlag)
}

func runCommand(cmd *cobra.Command, _ []string) {
	outputter := command.InitializeOutputter(cmd)
	defer outputter.WriteOutput()

	validators, err := params.getValidatorAccounts()
	if err != nil {
		outputter.SetError(fmt.Errorf("failed to get validator accounts: %w", err))

		return
	}

	manifest := &polybft.Manifest{GenesisValidators: validators}
	if err = manifest.Save(params.manifestPath); err != nil {
		outputter.SetError(fmt.Errorf("failed to save manifest file '%s': %w", params.manifestPath, err))

		return
	}

	outputter.SetCommandResult(params.getResult())
}

type manifestInitParams struct {
	manifestPath         string
	validatorsPath       string
	validatorsPrefixPath string
	premineValidators    string
	premineBalance       *big.Int
	stakeRaw             string
	stake                *big.Int
	validators           []string
}

func (p *manifestInitParams) validateFlags() error {
	var (
		stake          *big.Int
		premineBalance *big.Int
		err            error
	)

	if _, err = os.Stat(p.validatorsPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("provided validators path '%s' doesn't exist", p.validatorsPath)
	}

	if premineBalance, err = types.ParseUint256orHex(&p.premineValidators); err != nil {
		return fmt.Errorf("invalid premine validators balance provided '%s': %w", p.premineValidators, err)
	}

	if p.stakeRaw != "" {
		if stake, err = types.ParseUint256orHex(&p.stakeRaw); err != nil {
			return fmt.Errorf("invalid stake amount provided '%s': %w", p.premineValidators, err)
		}

		if stake.Cmp(premineBalance) > 0 {
			return fmt.Errorf("provided stake is greater than premine balance (stake=%s premine balance=%s)",
				stake.String(), premineBalance.String())
		}
	}

	p.premineBalance = premineBalance
	p.stake = stake

	return nil
}

// getValidatorAccounts gathers validator accounts info either from CLI or from provided local storage
func (p *manifestInitParams) getValidatorAccounts() ([]*polybft.Validator, error) {
	stake := p.stake
	// stake not provided => use validator balance as stake
	if stake == nil {
		stake = new(big.Int).Set(p.premineBalance)
	}

	if len(p.validators) > 0 {
		validators := make([]*polybft.Validator, len(p.validators))
		for i, validator := range p.validators {
			parts := strings.Split(validator, ":")

			if len(parts) != 3 {
				return nil, fmt.Errorf("expected 3 parts provided in the following format "+
					"<nodeId:ECDSA address:blsKey>, but got %d part(s)",
					len(parts))
			}

			if len(parts[0]) != nodeIDLength {
				return nil, fmt.Errorf("invalid node id: %s", parts[0])
			}

			if len(parts[1]) != ecdsaAddressLength {
				return nil, fmt.Errorf("invalid address: %s", parts[1])
			}

			if len(parts[2]) < blsKeyLength {
				return nil, fmt.Errorf("invalid bls key: %s", parts[2])
			}

			validators[i] = &polybft.Validator{
				NodeID:  parts[0],
				Address: types.StringToAddress(parts[1]),
				BlsKey:  parts[2],
				Balance: p.premineBalance,
				Stake:   stake,
			}
		}

		return validators, nil
	}

	validatorsPath := p.validatorsPath
	if validatorsPath == "" {
		validatorsPath = path.Dir(p.manifestPath)
	}

	validators, err := genesis.ReadValidatorsByPrefix(validatorsPath, p.validatorsPrefixPath)
	if err != nil {
		return nil, err
	}

	for _, v := range validators {
		v.Balance = p.premineBalance
		v.Stake = stake
	}

	return validators, nil
}

func (p *manifestInitParams) getResult() command.CommandResult {
	return &result{
		message: fmt.Sprintf("Manifest file written to %s\n", p.manifestPath),
	}
}

type result struct {
	message string
}

func (r *result) GetOutput() string {
	var buffer bytes.Buffer

	buffer.WriteString("\n[MANIFEST INITIALIZATION SUCCESS]\n")
	buffer.WriteString(r.message)

	return buffer.String()
}
