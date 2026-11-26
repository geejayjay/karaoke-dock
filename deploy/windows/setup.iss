; KaraokeDock installer for Windows (Inno Setup 6).
;
; It installs the app, then downloads what preparing songs needs while you
; watch: the helper programs on a download page with progress bars, then
; Python, the AI libraries and the AI models through "karaokedock setup".
; When it finishes, KaraokeDock is ready, with nothing left to download.
;
; Built by build-windows.ps1, which passes AppVersion and Stage.

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef Stage
  #define Stage "..\..\dist\windows\stage"
#endif
#ifndef OutDir
  #define OutDir "..\..\dist\windows"
#endif

[Setup]
AppId={{8E9D57C4-6B4A-49D1-9C43-3C0A7464B001}
AppName=KaraokeDock
AppVersion={#AppVersion}
AppPublisher=KaraokeDock
AppPublisherURL=https://github.com/geejayjay/karaoke-dock
DefaultDirName={localappdata}\Programs\KaraokeDock
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir={#OutDir}
OutputBaseFilename=KaraokeDock-Setup
SetupIconFile={#Stage}\karaokedock.ico
UninstallDisplayIcon={app}\KaraokeDock.exe
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes

[Files]
Source: "{#Stage}\*"; DestDir: "{app}"; Flags: recursesubdirs ignoreversion

[Icons]
Name: "{autoprograms}\KaraokeDock"; Filename: "{app}\KaraokeDock.exe"; Comment: "Start KaraokeDock and open Admin"
Name: "{autodesktop}\KaraokeDock"; Filename: "{app}\KaraokeDock.exe"; Comment: "Start KaraokeDock and open Admin"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"

[Run]
Filename: "{app}\KaraokeDock.exe"; Description: "Start KaraokeDock"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; What setup added beside the app. Songs and settings in the data folder are kept.
Type: filesandordirs; Name: "{app}\sidecar\.venv"
Type: filesandordirs; Name: "{app}\sidecar\.python"
Type: filesandordirs; Name: "{localappdata}\KaraokeDock\tools"

[Code]
var
  DownloadPage: TDownloadWizardPage;

procedure InitializeWizard;
begin
  DownloadPage := CreateDownloadPage('Downloading helper programs',
    'KaraokeDock uses FFmpeg, yt-dlp, Deno and uv to prepare songs.', nil);
end;

{ Downloads the helper programs, with progress bars, before the app is
  installed. The file names match windowsHelpers in setup.go. }
function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;
  if CurPageID <> wpReady then
    exit;
  DownloadPage.Clear;
  DownloadPage.Add('https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp.exe', 'helpers\yt-dlp.exe', '');
  DownloadPage.Add('https://github.com/denoland/deno/releases/latest/download/deno-x86_64-pc-windows-msvc.zip', 'helpers\deno.zip', '');
  DownloadPage.Add('https://github.com/astral-sh/uv/releases/latest/download/uv-x86_64-pc-windows-msvc.zip', 'helpers\uv.zip', '');
  DownloadPage.Add('https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl.zip', 'helpers\ffmpeg.zip', '');
  DownloadPage.Show;
  try
    try
      DownloadPage.Download;
    except
      if DownloadPage.AbortedByUser then
        Log('Download cancelled.')
      else
        SuppressibleMsgBox('A download didn''t finish: ' + AddPeriod(GetExceptionMessage) + #13#10#13#10 +
          'Check the internet connection and click Install to try again.', mbCriticalError, MB_OK, IDOK);
      Result := False;
    end;
  finally
    DownloadPage.Hide;
  end;
end;

{ After the files are in place, sets up Python, the AI libraries and the AI
  models in a window that shows what's happening. Offers to try again if it
  fails; the app also finishes any missing part when it starts. }
procedure CurStepChanged(CurStep: TSetupStep);
var
  Code: Integer;
begin
  if CurStep <> ssPostInstall then
    exit;
  WizardForm.StatusLabel.Caption := 'Installing Python, the AI libraries and AI models. This can take several minutes...';
  repeat
    if not Exec(ExpandConstant('{app}\bin\karaokedock.exe'), 'setup -from "' + ExpandConstant('{tmp}\helpers') + '"',
      ExpandConstant('{app}'), SW_SHOWNORMAL, ewWaitUntilTerminated, Code) then
      Code := -1;
    if Code = 0 then
      exit;
  until SuppressibleMsgBox('Setting up the AI part of KaraokeDock didn''t finish (code ' + IntToStr(Code) + ').' + #13#10#13#10 +
    'Click Retry to try again, or Cancel to finish now. KaraokeDock will try again when it starts.',
    mbError, MB_RETRYCANCEL, IDCANCEL) <> IDRETRY;
end;
