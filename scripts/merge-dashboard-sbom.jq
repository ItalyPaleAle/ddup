# Merges the CycloneDX SBOM of the dashboard, written by `pnpm sbom`, into the SBOM of the ddup binary, written by cyclonedx-gomod
# The binary embeds the dashboard, so its SBOM must list the dashboard's dependencies too
#
# Usage:
#   jq --slurpfile dashboard <dashboard SBOM> --arg version <release version> -f merge-dashboard-sbom.jq <binary SBOM>

($dashboard[0]) as $d
| if .specVersion != $d.specVersion then
    error("the binary SBOM uses CycloneDX \(.specVersion), but the dashboard SBOM uses \($d.specVersion)")
  else . end
| .metadata.component["bom-ref"] as $binaryRef
| $d.metadata.component["bom-ref"] as $oldDashboardRef
| ("ddup-dashboard@" + $version) as $dashboardRef
# The dashboard is a private package, so it gets the release version, and no npm package URL that scanners could match to an unrelated package
| ($d.metadata.component + {name: "ddup-dashboard", version: $version, "bom-ref": $dashboardRef} | del(.purl)) as $dashboardComponent
| .components += [$dashboardComponent] + $d.components
# The binary depends on the dashboard, which keeps its own dependency graph
| .dependencies = [
    (.dependencies[] | if .ref == $binaryRef then .dependsOn += [$dashboardRef] else . end),
    ($d.dependencies[] | if .ref == $oldDashboardRef then .ref = $dashboardRef else . end)
  ]
# Record the tool that generated the dashboard's part, in the same form as the binary SBOM's tools
| .metadata.tools |= (
    if type == "array" then
      . + [($d.metadata.tools.components // [])[] | {name, version}]
    else
      .components += ($d.metadata.tools.components // [])
    end
  )
