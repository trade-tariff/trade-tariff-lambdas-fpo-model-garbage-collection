{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    pre-commit-hooks = {
      url = "github:cachix/git-hooks.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      nixpkgs,
      flake-utils,
      pre-commit-hooks,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };

        lint = pkgs.writeScriptBin "lint" ''
          pre-commit run --all-files --show-diff-on-failure
        '';
        update-modules = pkgs.writeScriptBin "update-modules" ''
          cd collector
          go get -u ./...
        '';

        reviewTools = import (builtins.fetchTree {
          type = "github";
          owner = "NixOS";
          repo = "nixpkgs";
          rev = "d3b13ad75cc492690728f9307e443dec9ef9a20e";
          narHash = "sha256-pGPnZGi8y9d76xNeracniXO3dsYVROEmMkMc8jD0Ihc=";
        }) { inherit system; };
        reviewTrufflehogScan = pkgs.writeShellScript "trufflehog-pre-commit" ''
          set -euo pipefail
          exclude_file="$(mktemp)"
          trap 'rm -f "$exclude_file"' EXIT
          printf '%s\n' '(^|/)\.git(/|$)' '(^|/)\.terraform(/|$)' '(^|/)\.terragrunt-cache(/|$)' > "$exclude_file"
          while IFS= read -r ignored; do
            [ -n "$ignored" ] || continue
            escaped="$(printf '%s' "''${ignored%/}" | sed 's/[][(){}.^$*+?|\\]/\\&/g')"
            case "$ignored" in
              */) printf '(^|/)%s(/|$)\n' "$escaped" >> "$exclude_file" ;;
              *) printf '(^|/)%s$\n' "$escaped" >> "$exclude_file" ;;
            esac
          done < <(git ls-files --others --ignored --exclude-standard --directory)
          exec ${reviewTools.trufflehog}/bin/trufflehog filesystem "$(pwd -P)" --exclude-paths="$exclude_file" --results=verified,unknown --fail
        '';
        reviewPreCommitConfig = pkgs.writeText "review-pre-commit.json" (
          builtins.toJSON {
            repos = [
              {
                repo = "local";
                hooks = [
                  {
                    id = "trailing-whitespace";
                    name = "trim trailing whitespace";
                    language = "system";
                    entry = "${reviewTools.python3Packages.pre-commit-hooks}/bin/trailing-whitespace-fixer";
                    types = [ "text" ];
                    stages = [
                      "pre-commit"
                      "pre-push"
                      "manual"
                    ];
                  }
                  {
                    id = "end-of-file-fixer";
                    name = "fix end of files";
                    language = "system";
                    entry = "${reviewTools.python3Packages.pre-commit-hooks}/bin/end-of-file-fixer";
                    types = [ "text" ];
                    stages = [
                      "pre-commit"
                      "pre-push"
                      "manual"
                    ];
                  }
                  {
                    id = "check-yaml";
                    name = "check yaml";
                    language = "system";
                    entry = "${reviewTools.python3Packages.pre-commit-hooks}/bin/check-yaml";
                    types = [ "yaml" ];
                  }
                  {
                    id = "markdownlint-docker";
                    name = "markdownlint-docker";
                    language = "system";
                    entry = "${reviewTools.markdownlint-cli}/bin/markdownlint";
                    types = [ "markdown" ];
                    args = [
                      "--ignore"
                      "terraform"
                      "--fix"
                    ];
                  }
                  {
                    id = "golangci-lint-full";
                    name = "golangci-lint-full";
                    language = "system";
                    entry = "${reviewTools.bash}/bin/bash -c 'cd collector && golangci-lint run ./...'";
                    types = [ "go" ];
                    require_serial = true;
                    pass_filenames = false;
                  }
                  {
                    id = "actionlint-docker";
                    name = "Lint GitHub Actions workflow files";
                    language = "system";
                    entry = "${reviewTools.actionlint}/bin/actionlint";
                    types = [ "yaml" ];
                    files = "^\\.github/workflows/";
                  }
                  {
                    id = "trufflehog";
                    name = "TruffleHog";
                    language = "system";
                    entry = "${reviewTrufflehogScan}";
                    stages = [
                      "pre-commit"
                      "pre-push"
                    ];
                  }
                ];
              }
            ];
          }
        );
        reviewEnvironment = ''
          if [ "''${REVIEW_SANDBOX:-}" != 1 ] || [ "$PWD" != /workspace ]; then exit 64; fi
          export TMPDIR=/workspace/.review/tmp PRE_COMMIT_HOME=/workspace/.review/pre-commit
          export GOPATH=/workspace/.review/go GOMODCACHE=/workspace/.review/go-modules
          export GOCACHE=/workspace/.review/go-build GOLANGCI_LINT_CACHE=/workspace/.review/go-lint
          export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOFLAGS="-mod=readonly -p=4" DRY_RUN=true GOMAXPROCS=4
          mkdir -p "$TMPDIR" "$GOMODCACHE" "$GOCACHE" "$GOLANGCI_LINT_CACHE"
        '';

        preCommitCheck = pre-commit-hooks.lib.${system}.run {
          src = ./.;
          configPath = ".pre-commit-config-nix.yaml";
          default_stages = [ "pre-commit" ];
          hooks = {
            actionlint = {
              enable = true;
              stages = [ "pre-commit" ];
            };
            check-added-large-files = {
              enable = true;
              stages = [ "pre-commit" ];
            };
            check-case-conflicts = {
              enable = true;
              stages = [ "pre-commit" ];
            };
            check-merge-conflicts = {
              enable = true;
              stages = [ "pre-commit" ];
            };
            check-yaml = {
              enable = true;
              stages = [ "pre-commit" ];
            };
            deadnix = {
              enable = true;
              stages = [ "pre-commit" ];
            };
            detect-private-keys = {
              enable = true;
              stages = [ "pre-commit" ];
            };
            end-of-file-fixer = {
              enable = true;
              stages = [ "pre-commit" ];
            };
            markdownlint = {
              enable = true;
              excludes = [ "^terraform/" ];
              stages = [ "pre-commit" ];
            };
            nixfmt = {
              enable = true;
              stages = [ "pre-commit" ];
            };
            statix = {
              enable = true;
              settings.ignore = [ ".direnv" ];
              stages = [ "pre-commit" ];
            };
            trim-trailing-whitespace = {
              enable = true;
              stages = [ "pre-commit" ];
            };
            trufflehog = {
              enable = true;
              stages = [ "pre-commit" ];
            };

            golangci-lint = {
              enable = true;
              name = "golangci-lint";
              description = "Run golangci-lint in collector";
              entry = "bash -c 'cd collector && golangci-lint run ./...'";
              files = "^collector/.*\\.go$";
              pass_filenames = false;
              stages = [ "pre-commit" ];
            };
          };
        };
      in
      {
        devShells.review = pkgs.mkShell {
          shellHook = "";
          nativeBuildInputs = [
            reviewTools.go_1_27
            reviewTools.golangci-lint
            pkgs.gitMinimal
            pkgs.bash
            pkgs.coreutils
            pkgs.gnugrep
            pkgs.gnused
            pkgs.jq
            reviewTools.pre-commit
            (pkgs.writeShellApplication {
              name = "review-bootstrap";
              text = reviewEnvironment + ''
                cp -a /deps/go-modules/. "$GOMODCACHE/"
                cd collector
                go mod verify
              '';
            })
            (pkgs.writeShellApplication {
              name = "review-check";
              text = reviewEnvironment + ''
                status=0
                pre-commit run --all-files --config ${reviewPreCommitConfig} || status=$?
                cd collector
                go test -json ./... | tee "$TMPDIR/unit.json" || status=$?
                jq -e -s 'any(.[]; .Action == "pass" and has("Test")) and all(.[]; .Action != "fail" and (.Action != "skip" or (has("Test") | not)))' "$TMPDIR/unit.json" || status=$?
                CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -ldflags="-s -w" -o "$TMPDIR/bootstrap" . || status=$?
                exit "$status"
              '';
            })
          ];
          TZDIR = "${pkgs.tzdata.outPath}/share/zoneinfo";
          SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
        };

        devShells.default = pkgs.mkShell {
          shellHook = ''
            ${preCommitCheck.shellHook}
            export PATH=${pkgs.writeShellScriptBin "pre-commit" ''
              set -euo pipefail

              has_config=false
              for arg in "$@"; do
                case "$arg" in
                  -c|--config|--config=*)
                    has_config=true
                    ;;
                esac
              done

              if [ "$has_config" = true ]; then
                exec ${preCommitCheck.config.package}/bin/pre-commit "$@"
              fi

              if [ "''${1:-}" = "run" ]; then
                shift
                exec ${preCommitCheck.config.package}/bin/pre-commit run --config .pre-commit-config-nix.yaml "$@"
              fi

              exec ${preCommitCheck.config.package}/bin/pre-commit "$@"
            ''}/bin:$PATH
          '';

          buildInputs =
            preCommitCheck.enabledPackages
            ++ (with pkgs; [
              circleci-cli
              go_1_27
              golangci-lint
              gopls
              lint
              nodejs
              update-modules
            ]);
        };
      }
    );
}
