{
  description = "GitHub webhook relay for NixOS";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      supportedSystems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      packages = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.buildGoModule {
            pname = "github-relay";
            version = "0.1.0";
            src = ./.;
            vendorHash = null;
            subPackages = [ "cmd/relay" ];

            postInstall = ''
              mv $out/bin/relay $out/bin/github-relay
            '';
          };
        }
      );

      nixosModules.default = import ./module.nix self;
    };
}
