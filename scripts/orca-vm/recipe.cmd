@echo off
rem Windows entry point of the Orca environment recipe "docker-devcontainer":
rem
rem   "./scripts/orca-vm/recipe" create|suspend|resume|destroy
rem
rem Orca runs the orca.yaml command through cmd.exe on Windows, which resolves
rem it to this file. It runs scripts\orca-vm\docker-<step>.sh with Git Bash:
rem ORCA_DOCKER_BASH, else the bash.exe of the Git for Windows on PATH, else
rem the default install locations. A bare "bash" is never used, because on
rem Windows that is often WSL's bash.exe, which cannot drive Docker Desktop
rem with Windows paths. stdin (the lifecycle payload) and stdout (the recipe
rem result) pass through unchanged.
setlocal EnableExtensions DisableDelayedExpansion
set "STEP=%~1"
if /i "%STEP%"=="create" goto find_bash
if /i "%STEP%"=="suspend" goto find_bash
if /i "%STEP%"=="resume" goto find_bash
if /i "%STEP%"=="destroy" goto find_bash
>&2 echo usage: recipe create^|suspend^|resume^|destroy
exit /b 2

:find_bash
rem Forward slashes, so that dirname works inside bash.
set "SCRIPT=%~dp0docker-%STEP%.sh"
set "SCRIPT=%SCRIPT:\=/%"
set "GIT_BASH=%ORCA_DOCKER_BASH%"
if defined GIT_BASH goto run
for /f "delims=" %%G in ('where git.exe 2^>nul') do call :from_git "%%~dpG"
if not defined GIT_BASH if exist "%ProgramFiles%\Git\bin\bash.exe" set "GIT_BASH=%ProgramFiles%\Git\bin\bash.exe"
if not defined GIT_BASH if exist "%LOCALAPPDATA%\Programs\Git\bin\bash.exe" set "GIT_BASH=%LOCALAPPDATA%\Programs\Git\bin\bash.exe"
if defined GIT_BASH goto run
>&2 echo orca-docker: Git Bash not found. Install Git for Windows or set ORCA_DOCKER_BASH to its bin\bash.exe.
exit /b 127

:run
"%GIT_BASH%" "%SCRIPT%"
exit /b %ERRORLEVEL%

rem git.exe lives in <Git>\cmd, <Git>\bin or <Git>\mingw64\bin.
:from_git
if defined GIT_BASH exit /b 0
if exist "%~1..\bin\bash.exe" set "GIT_BASH=%~1..\bin\bash.exe" & exit /b 0
if exist "%~1..\..\bin\bash.exe" set "GIT_BASH=%~1..\..\bin\bash.exe"
exit /b 0
