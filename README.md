**UPDATE:** Since this project was created bors-ng has added
`required_approvals` which superseedes the functionality that this project
provides

Four Eyes
=========
"Four Eyes" is a Github bot which approves
[bors-ng](https://github.com/bors-ng/bors-ng) merge commits only if all pull
requests in the merge commit have been approved by another person than the pull
request author.

The application is made to be hosted on AppEngine, but can easily be modified
to be run standalone somewhere.


Setup
-----
1. Create a new Github App on your organization.
  * Permissions:
    * Commit status: Read/write, to be able to set the status.
    * Pull requests: Read-only, to be able to query for pull requests comments.
    * Repository content: Read-only, to be able to query commit messages.
  * Webhook events:
    * Push
2. Copy `vars.go.template` to `vars.go` and set the missing information.
3. Push to the `app.yaml` file to Google AppEngine.
4. Add `tink/four-eyes` to the `status` list in `bors.toml`.

Developing
----------
Before doing anything else you must set `$GOPATH` to the root of the project:g

    export GOPATH=$(pwd)
 
 then install [`godep`](https://github.com/tools/godep) and execute

    $ cd src/four-eyes
    $ dep ensure

to sync your `vendor` directory. Then install Google AppEngine SDK. Start a
local development environment:

    $ cd ../../appengine
    $ dev_appserver.py --enable_host_checking=False app.yaml

(You need `--enable_host_checking=False` for `ngrok` mentioned in next
sentence.) You likely want to create a temporary Github App for development and
use something like [`ngrok`](https://ngrok.com) to get a temporary URL that
Github can send its webhooks to.

Testing
-------
There's a very basic test. Run using `go test -v .`.

Limitations
-----------
 * The bot parses the commit message format of the bors-ng merge commit to
   discover all pull requests it merges. If bors-ng decides to change the
   format, it might fail parsing. That said, security-wise it will only fail
   tests, not pass them.
