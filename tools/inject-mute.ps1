Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public class K {
  [DllImport("user32.dll")] public static extern void keybd_event(byte bVk, byte bScan, uint dwFlags, UIntPtr dwExtraInfo);
}
'@
[K]::keybd_event(0xAD,0,0,[UIntPtr]::Zero)   # VK_VOLUME_MUTE down
Start-Sleep -Milliseconds 200
[K]::keybd_event(0xAD,0,2,[UIntPtr]::Zero)   # KEYEVENTF_KEYUP
Write-Host "injected VK_VOLUME_MUTE"
