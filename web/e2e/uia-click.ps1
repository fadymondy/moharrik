# Presses a button in a native Chromium dialog through Windows UI Automation (MH-483).
#
#   powershell -File uia-click.ps1 -Button Allow [-TimeoutSec 20] [-ExePath <chrome.exe>] [-List]
#
# Chrome's permission prompt is browser UI, not a page: no script inside the
# browser can answer it, which is the point of it. A check that must prove the
# REAL "Connect to the site" click grants every site answers it from outside,
# as a person would. Only windows of the given Chromium binary are searched.
param(
  [string]$Button = "Allow",
  [int]$TimeoutSec = 20,
  [string]$ExePath = "chromium-1243",
  [switch]$List
)
Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes
$A = [System.Windows.Automation.AutomationElement]
$pids = @(Get-CimInstance Win32_Process -Filter "Name='chrome.exe'" | Where-Object { $_.ExecutablePath -like "*$ExePath*" } | ForEach-Object { [int]$_.ProcessId })
if ($pids.Count -eq 0) { Write-Output "no-chromium"; exit 2 }
$deadline = (Get-Date).AddSeconds($TimeoutSec)
$cond = New-Object System.Windows.Automation.AndCondition(
  (New-Object System.Windows.Automation.PropertyCondition($A::ControlTypeProperty, [System.Windows.Automation.ControlType]::Button)),
  (New-Object System.Windows.Automation.PropertyCondition($A::NameProperty, $Button))
)
while ((Get-Date) -lt $deadline) {
  $wins = $A::RootElement.FindAll([System.Windows.Automation.TreeScope]::Children, [System.Windows.Automation.Condition]::TrueCondition)
  foreach ($w in $wins) {
    if ($pids -notcontains $w.Current.ProcessId) { continue }
    if ($List) {
      $all = $w.FindAll([System.Windows.Automation.TreeScope]::Descendants,
        (New-Object System.Windows.Automation.PropertyCondition($A::ControlTypeProperty, [System.Windows.Automation.ControlType]::Button)))
      foreach ($b in $all) { Write-Output ("[" + $w.Current.Name + "] button: " + $b.Current.Name) }
      continue
    }
    $found = $w.FindFirst([System.Windows.Automation.TreeScope]::Descendants, $cond)
    if ($found) {
      $found.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern).Invoke()
      Write-Output ("pressed '" + $Button + "' in '" + $w.Current.Name + "'")
      exit 0
    }
  }
  if ($List) { exit 0 }
  Start-Sleep -Milliseconds 300
}
Write-Output "not-found"
exit 1
