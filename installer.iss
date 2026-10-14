; Script generated for Inno Setup 6
; WinAutoSort - Lightweight Background File Router for Windows

#define MyAppName "WinAutoSort"
#define MyAppVersion "1.0.0"
#define MyAppPublisher "reandri-1"
#define MyAppURL "https://github.com/reandri-1/winautosort"
#define MyAppExeName "WinAutoSort.exe"

[Setup]
AppId={{5E97D4B6-613A-4F8A-9E9C-8A1E6589B742}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
DefaultDirName={localappdata}\Programs\{#MyAppName}
DisableProgramGroupPage=yes
; Per-user install: Tidak butuh administrator UAC, langsung bisa diinstall oleh siapa saja
PrivilegesRequired=lowest
OutputDir=dist
OutputBaseFilename=WinAutoSort-Setup-v{#MyAppVersion}
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
ArchitecturesInstallIn64BitMode=x64compatible
UninstallDisplayIcon={app}\{#MyAppExeName}
CloseApplications=yes

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "startupicon"; Description: "Jalankan otomatis saat Windows dinyalakan (Auto-start on Windows boot)"; GroupDescription: "Pengaturan Startup:"; Flags: unchecked
Name: "desktopicon"; Description: "Buat shortcut di Desktop"; GroupDescription: "Shortcut tambahan:"; Flags: unchecked

[Files]
Source: "{#MyAppExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "config.json"; DestDir: "{app}"; Flags: onlyifdoesntexist uninsneveruninstall
Source: "README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "LICENSE"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon
Name: "{autostartup}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: startupicon

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "Jalankan {#MyAppName} sekarang"; Flags: nowait postinstall skipifsilent

[InstallDelete]
; Tutup instance lama jika sedang berjalan sebelum file ditimpa
Type: files; Name: "{app}\{#MyAppExeName}.old"

[Code]
// Hentikan proses WinAutoSort.exe jika sedang berjalan sebelum instalasi/uninstalasi
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
begin
  Exec('taskkill.exe', '/F /IM WinAutoSort.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Result := '';
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
begin
  if CurUninstallStep = usUninstall then
  begin
    Exec('taskkill.exe', '/F /IM WinAutoSort.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;
