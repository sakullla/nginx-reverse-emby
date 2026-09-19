package plugins

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

const machOExecuteProtection = 0x4

const (
	machOLoadMain     = uint32(0x80000028)
	machOX86Thread64  = uint32(4)
	machOARMThread64  = uint32(6)
	machOX86ThreadRIP = 16
	machOARMThreadPC  = 32
)

func validateRPCExecutable(name string, artifact Artifact) error {
	switch artifact.GOOS {
	case "linux", "freebsd":
		return validateELFExecutable(name, artifact.GOOS, artifact.GOARCH)
	case "windows":
		return validatePEExecutable(name, artifact.GOARCH)
	case "darwin":
		return validateMachOExecutable(name, artifact.GOARCH)
	default:
		return errors.New("unsupported artifact platform")
	}
}

func validateELFExecutable(name, goos, goarch string) error {
	file, err := elf.Open(name)
	if err != nil {
		return fmt.Errorf("Unix RPC artifact is not a structurally valid ELF executable: %w", err)
	}
	defer file.Close()

	if file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB {
		return errors.New("ELF executable must use the 64-bit little-endian format")
	}
	if (goos == "linux" && file.OSABI != elf.ELFOSABI_NONE && file.OSABI != elf.ELFOSABI_LINUX) || (goos == "freebsd" && file.OSABI != elf.ELFOSABI_FREEBSD) {
		return errors.New("ELF OS ABI does not match declared GOOS")
	}
	wantMachine, err := elfMachine(goarch)
	if err != nil {
		return err
	}
	if file.Machine != wantMachine {
		return errors.New("ELF machine does not match declared GOARCH")
	}
	if file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN {
		return fmt.Errorf("ELF artifact type %s is not an executable image", file.Type)
	}
	if file.Entry == 0 {
		return errors.New("ELF executable has no entry point")
	}

	size, err := regularFileSize(name)
	if err != nil {
		return err
	}
	hasExecutableEntry := false
	for _, program := range file.Progs {
		if !fileRangeWithin(program.Off, program.Filesz, size) || program.Filesz > program.Memsz {
			return errors.New("ELF program segment exceeds artifact boundaries")
		}
		if program.Type == elf.PT_LOAD && program.Flags&elf.PF_X != 0 && addressWithin(file.Entry, program.Vaddr, program.Filesz) {
			hasExecutableEntry = true
		}
	}
	for _, section := range file.Sections {
		if section.Type != elf.SHT_NOBITS && !fileRangeWithin(section.Offset, section.FileSize, size) {
			return fmt.Errorf("ELF section %q exceeds artifact boundaries", section.Name)
		}
	}
	if !hasExecutableEntry {
		return errors.New("ELF entry point is not contained in a file-backed executable load segment")
	}
	return nil
}

func validatePEExecutable(name, goarch string) error {
	file, err := pe.Open(name)
	if err != nil {
		return fmt.Errorf("Windows RPC artifact is not a structurally valid PE executable: %w", err)
	}
	defer file.Close()

	wantMachine, err := peMachine(goarch)
	if err != nil {
		return err
	}
	if file.Machine != wantMachine {
		return errors.New("PE machine does not match declared GOARCH")
	}
	if file.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 {
		return errors.New("PE artifact is not marked as an executable image")
	}
	if file.Characteristics&pe.IMAGE_FILE_DLL != 0 {
		return errors.New("PE DLL artifacts are not executable RPC services")
	}
	if file.Characteristics&pe.IMAGE_FILE_SYSTEM != 0 {
		return errors.New("PE system images are not user-mode RPC services")
	}
	optional, ok := file.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return errors.New("PE executable must have a complete PE32+ optional header")
	}
	if optional.AddressOfEntryPoint == 0 {
		return errors.New("PE executable has no entry point")
	}
	if optional.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI && optional.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_CUI {
		return fmt.Errorf("PE subsystem %d cannot be launched as a user-mode RPC service", optional.Subsystem)
	}

	size, err := regularFileSize(name)
	if err != nil {
		return err
	}
	if optional.SizeOfHeaders == 0 || uint64(optional.SizeOfHeaders) > size || optional.SizeOfImage == 0 {
		return errors.New("PE image header exceeds artifact boundaries")
	}
	hasExecutableEntry := false
	for _, section := range file.Sections {
		if !fileRangeWithin(uint64(section.Offset), uint64(section.Size), size) {
			return fmt.Errorf("PE section %q exceeds artifact boundaries", section.Name)
		}
		if section.Characteristics&pe.IMAGE_SCN_MEM_EXECUTE != 0 && addressWithin(uint64(optional.AddressOfEntryPoint), uint64(section.VirtualAddress), uint64(section.Size)) {
			hasExecutableEntry = true
		}
	}
	if !hasExecutableEntry {
		return errors.New("PE entry point is not contained in a file-backed executable section")
	}
	return nil
}

func validateMachOExecutable(name, goarch string) error {
	file, err := macho.Open(name)
	if err != nil {
		return fmt.Errorf("Darwin RPC artifact is not a structurally valid Mach-O executable: %w", err)
	}
	defer file.Close()

	wantCPU, err := machoCPU(goarch)
	if err != nil {
		return err
	}
	if file.Cpu != wantCPU {
		return errors.New("Mach-O CPU does not match declared GOARCH")
	}
	if file.Type != macho.TypeExec {
		return fmt.Errorf("Mach-O artifact type %s is not an executable image", file.Type)
	}
	if file.Magic != macho.Magic64 {
		return errors.New("Mach-O executable must use the 64-bit format")
	}

	size, err := regularFileSize(name)
	if err != nil {
		return err
	}
	hasExecutableSegment := false
	segments := make([]*macho.Segment, 0, len(file.Loads))
	for _, load := range file.Loads {
		segment, ok := load.(*macho.Segment)
		if !ok {
			continue
		}
		if !fileRangeWithin(segment.Offset, segment.Filesz, size) || segment.Filesz > segment.Memsz {
			return fmt.Errorf("Mach-O segment %q exceeds artifact boundaries", segment.Name)
		}
		if segment.Prot&machOExecuteProtection != 0 && segment.Filesz != 0 {
			hasExecutableSegment = true
		}
		segments = append(segments, segment)
	}
	for _, section := range file.Sections {
		segment := file.Segment(section.Seg)
		if segment == nil || !addressRangeWithin(section.Addr, section.Size, segment.Addr, segment.Memsz) {
			return fmt.Errorf("Mach-O section %q is outside its segment", section.Name)
		}
		if isMachOZeroFill(section.Flags) {
			continue
		}
		if uint64(section.Offset) < segment.Offset || !fileRangeWithin(uint64(section.Offset), section.Size, size) || !fileRangeWithin(uint64(section.Offset)-segment.Offset, section.Size, segment.Filesz) {
			return fmt.Errorf("Mach-O section %q exceeds artifact boundaries", section.Name)
		}
	}
	if !hasExecutableSegment {
		return errors.New("Mach-O executable has no file-backed executable segment")
	}
	if err := validateMachOEntryPoint(file, segments); err != nil {
		return err
	}
	return nil
}

func validateMachOEntryPoint(file *macho.File, segments []*macho.Segment) error {
	found := false
	for _, load := range file.Loads {
		raw := load.Raw()
		if len(raw) < 8 {
			return errors.New("Mach-O load command is truncated")
		}
		command := file.ByteOrder.Uint32(raw[:4])
		switch command {
		case machOLoadMain:
			if found {
				return errors.New("Mach-O executable has multiple entry-point commands")
			}
			if len(raw) != 24 || file.ByteOrder.Uint32(raw[4:8]) != 24 {
				return errors.New("Mach-O LC_MAIN command is truncated")
			}
			entryOffset := file.ByteOrder.Uint64(raw[8:16])
			if !machOFileOffsetIsExecutable(entryOffset, segments) {
				return errors.New("Mach-O LC_MAIN entry is outside file-backed executable segments")
			}
			found = true
		case uint32(macho.LoadCmdUnixThread):
			if found {
				return errors.New("Mach-O executable has multiple entry-point commands")
			}
			entryAddress, err := machOUnixThreadEntry(file.ByteOrder, file.Cpu, raw)
			if err != nil {
				return err
			}
			if !machOVirtualAddressIsFileBackedExecutable(entryAddress, segments) {
				return errors.New("Mach-O LC_UNIXTHREAD entry is outside file-backed executable segments")
			}
			found = true
		}
	}
	if !found {
		return errors.New("Mach-O executable has no LC_MAIN or LC_UNIXTHREAD entry point")
	}
	return nil
}

func machOUnixThreadEntry(order binary.ByteOrder, cpu macho.Cpu, raw []byte) (uint64, error) {
	if len(raw) < 16 {
		return 0, errors.New("Mach-O LC_UNIXTHREAD command is truncated")
	}
	flavor := order.Uint32(raw[8:12])
	count := uint64(order.Uint32(raw[12:16]))
	if count*4 != uint64(len(raw)-16) {
		return 0, errors.New("Mach-O LC_UNIXTHREAD state exceeds its command boundary")
	}
	register := 0
	wantFlavor := uint32(0)
	switch cpu {
	case macho.CpuAmd64:
		register, wantFlavor = machOX86ThreadRIP, machOX86Thread64
	case macho.CpuArm64:
		register, wantFlavor = machOARMThreadPC, machOARMThread64
	default:
		return 0, errors.New("unsupported Mach-O thread-state architecture")
	}
	registerOffset := 16 + register*8
	if flavor != wantFlavor || registerOffset+8 > len(raw) || uint64(registerOffset-16+8) > count*4 {
		return 0, errors.New("Mach-O LC_UNIXTHREAD lacks the required 64-bit entry register")
	}
	return order.Uint64(raw[registerOffset : registerOffset+8]), nil
}

func machOFileOffsetIsExecutable(entry uint64, segments []*macho.Segment) bool {
	for _, segment := range segments {
		if segment.Prot&machOExecuteProtection != 0 && addressWithin(entry, segment.Offset, segment.Filesz) {
			return true
		}
	}
	return false
}

func machOVirtualAddressIsFileBackedExecutable(entry uint64, segments []*macho.Segment) bool {
	for _, segment := range segments {
		if segment.Prot&machOExecuteProtection != 0 && addressWithin(entry, segment.Addr, segment.Filesz) {
			return true
		}
	}
	return false
}

func elfMachine(goarch string) (elf.Machine, error) {
	switch goarch {
	case "amd64":
		return elf.EM_X86_64, nil
	case "arm64":
		return elf.EM_AARCH64, nil
	default:
		return 0, errors.New("unsupported ELF architecture")
	}
}

func peMachine(goarch string) (uint16, error) {
	switch goarch {
	case "amd64":
		return pe.IMAGE_FILE_MACHINE_AMD64, nil
	case "arm64":
		return pe.IMAGE_FILE_MACHINE_ARM64, nil
	default:
		return 0, errors.New("unsupported PE architecture")
	}
}

func machoCPU(goarch string) (macho.Cpu, error) {
	switch goarch {
	case "amd64":
		return macho.CpuAmd64, nil
	case "arm64":
		return macho.CpuArm64, nil
	default:
		return 0, errors.New("unsupported Mach-O architecture")
	}
}

func regularFileSize(name string) (uint64, error) {
	info, err := os.Stat(name)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 {
		return 0, errors.New("RPC artifact must be a regular file")
	}
	return uint64(info.Size()), nil
}

func fileRangeWithin(offset, length, size uint64) bool {
	return offset <= size && length <= size-offset
}

func addressWithin(address, start, size uint64) bool {
	return size != 0 && address >= start && address-start < size
}

func addressRangeWithin(address, length, start, size uint64) bool {
	return address >= start && length <= size && address-start <= size-length
}

func isMachOZeroFill(flags uint32) bool {
	switch flags & 0xff {
	case 0x1, 0xc, 0x12: // S_ZEROFILL, S_GB_ZEROFILL, S_THREAD_LOCAL_ZEROFILL
		return true
	default:
		return false
	}
}
