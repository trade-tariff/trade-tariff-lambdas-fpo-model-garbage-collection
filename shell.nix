let
  pkgs = import <nixpkgs> {};
in
pkgs.mkShell {
  nativeBuildInputs = with pkgs; [
    circleci-cli
    delve
    go
    go-outline
    go-tools
    gopkgs
    gopls
  ];

  hardeningDisable = ["all"];
}
