# Generate prestate

To test out the various changes to the firehose tracer, you can run the `generate-prestate` subcommend here. Currently, only op stack blockchains are supported.

```bash
export ARCHIVE_ENDPOINT="rpc_archive_node_endpoint"

# generate-prestate <network (optMainnet or baseMainnet)> <transaction_id>
# the below example run was used to create the prestate.json file for the deposit_nonce_check_optimism_after_canyon test
go run ./generate-prestate optMainnet 0xb77e56d591aab27502548d4d85aff6c1f835c1e4e820b2360751209516a63472 > testdata/TestFirehosePrestate/deposit_nonce_check_optimism_after_canyon/prestate.json
```

Then modify the method `TestFirehosePrestate` in `firehose_test.go` and add the new test

```bash
# the below command will create a file such as block.{blockNumber}.golden.json
# validation needs to be done between the created file and an RPC endpoint to make sure the files are on par
GOLDEN_UPDATE=true go test ./... -run "TestFirehosePrestate/deposit_nonce_check_optimism_after_canyon"
```
