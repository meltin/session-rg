{
  description = "Search past Claude Code sessions with ripgrep";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { self, nixpkgs }:
    let
      forEachSystem = nixpkgs.lib.genAttrs [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
    in
    {
      packages = forEachSystem (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          session-rg = pkgs.buildGoModule {
            pname = "session-rg";
            version = "0.1.0";
            src = ./.;
            vendorHash = null;
            nativeBuildInputs = [ pkgs.makeWrapper ];
            nativeCheckInputs = [ pkgs.ripgrep ];
            # rg on the user's PATH wins; this one is the fallback
            postInstall = ''
              wrapProgram $out/bin/session-rg --suffix PATH : ${pkgs.lib.makeBinPath [ pkgs.ripgrep ]}
            '';
            meta = {
              description = "Search past Claude Code sessions with ripgrep";
              homepage = "https://github.com/meltin/session-rg";
              license = pkgs.lib.licenses.mit;
              mainProgram = "session-rg";
            };
          };
        in
        {
          inherit session-rg;
          default = session-rg;
        }
      );
    };
}
