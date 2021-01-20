package command

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/elastic/go-concert/ctxtool/osctx"
	"github.com/elastic/go-concert/timed"
	"github.com/spf13/cobra"
	"go.uber.org/multierr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sys/unix"

	"github.com/andrewkroh/stream/pkg/output"

	// Register outputs.
	_ "github.com/andrewkroh/stream/pkg/output/tcp"
	_ "github.com/andrewkroh/stream/pkg/output/tls"
	_ "github.com/andrewkroh/stream/pkg/output/udp"
)

func Execute() error {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-c
		cancel()
	}()

	return ExecuteContext(ctx)
}

func ExecuteContext(ctx context.Context) error {
	logger, err := logger()
	if err != nil {
		return nil
	}

	rootCmd := &cobra.Command{Use: "stream", SilenceUsage: true}

	// Global flags.
	var opts output.Options
	rootCmd.PersistentFlags().StringVar(&opts.Addr, "addr", "", "destination address")
	rootCmd.PersistentFlags().DurationVar(&opts.Delay, "delay", 0, "delay start after start-signal")
	rootCmd.PersistentFlags().StringVarP(&opts.Protocol, "protocol", "p", "tcp", "protocol (tcp/udp/tls)")
	rootCmd.PersistentFlags().IntVar(&opts.Retries, "retry", 10, "connection retry attempts for tcp based protocols")
	rootCmd.PersistentFlags().StringVarP(&opts.StartSignal, "start-signal", "s", "", "wait for start signal")

	// Sub-commands.
	rootCmd.AddCommand(newLogRunner(&opts, logger))
	rootCmd.AddCommand(newPCAPRunner(&opts, logger))
	rootCmd.AddCommand(versionCmd)

	// Add common start-up delay logic.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return multierr.Combine(
			waitForStartSignal(&opts, cmd.Context(), logger),
			waitForDelay(&opts, cmd.Context(), logger),
		)
	}

	return rootCmd.ExecuteContext(ctx)
}

func logger() (*zap.Logger, error) {
	conf := zap.NewProductionConfig()
	conf.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	conf.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	log, err := conf.Build()
	if err != nil {
		return nil, err
	}
	return log, nil
}

func waitForStartSignal(opts *output.Options, parent context.Context, logger *zap.Logger) error {
	if opts.StartSignal == "" {
		return nil
	}

	num := unix.SignalNum(opts.StartSignal)
	if num == 0 {
		return fmt.Errorf("unknown signal %v", opts.StartSignal)
	}

	// Wait for the signal or the command context to be done.
	logger.Sugar().Infow("Waiting for signal.", "start-signal", opts.StartSignal)
	startCtx, _ := osctx.WithSignal(parent, os.Signal(num))
	<-startCtx.Done()
	return nil
}

func waitForDelay(opts *output.Options, parent context.Context, logger *zap.Logger) error {
	if opts.Delay <= 0 {
		return nil
	}

	logger.Sugar().Info("Delaying connection.", "delay", opts.Delay)
	if err := timed.Wait(parent, opts.Delay); err != nil {
		return fmt.Errorf("delay waiting period was interrupted: %w", err)
	}
	return nil
}
