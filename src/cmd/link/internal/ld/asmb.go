// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/link/internal/loader"
	"fmt"
	"sync"
)

// Assembling the binary is broken into two steps:
//  - writing out the code/data/dwarf Segments
//  - writing out the architecture specific pieces.
// This function handles the first part.
func asmb(ctxt *Link, ldr *loader.Loader) {
	// TODO(jfaller): delete me.
	if thearch.Asmb != nil {
		thearch.Asmb(ctxt, ldr)
		return
	}

	if ctxt.IsELF {
		Asmbelfsetup()
	}

	var wg sync.WaitGroup
	sect := Segtext.Sections[0]
	offset := sect.Vaddr - Segtext.Vaddr + Segtext.Fileoff
	f := func(ctxt *Link, out *OutBuf, start, length int64) {
		pad := thearch.CodePad
		if pad == nil {
			pad = zeros[:]
		}
		CodeblkPad(ctxt, out, start, length, pad)
	}

	if !thearch.WriteTextBlocks {
		writeParallel(&wg, f, ctxt, offset, sect.Vaddr, sect.Length)
		for _, sect := range Segtext.Sections[1:] {
			offset := sect.Vaddr - Segtext.Vaddr + Segtext.Fileoff
			writeParallel(&wg, Datblk, ctxt, offset, sect.Vaddr, sect.Length)
		}
	} else {
		// TODO why can't we handle all sections this way?
		for _, sect := range Segtext.Sections {
			offset := sect.Vaddr - Segtext.Vaddr + Segtext.Fileoff
			// Handle additional text sections with Codeblk
			if sect.Name == ".text" {
				writeParallel(&wg, f, ctxt, offset, sect.Vaddr, sect.Length)
			} else {
				writeParallel(&wg, Datblk, ctxt, offset, sect.Vaddr, sect.Length)
			}
		}
	}

	if Segrodata.Filelen > 0 {
		writeParallel(&wg, Datblk, ctxt, Segrodata.Fileoff, Segrodata.Vaddr, Segrodata.Filelen)
	}

	if Segrelrodata.Filelen > 0 {
		writeParallel(&wg, Datblk, ctxt, Segrelrodata.Fileoff, Segrelrodata.Vaddr, Segrelrodata.Filelen)
	}

	writeParallel(&wg, Datblk, ctxt, Segdata.Fileoff, Segdata.Vaddr, Segdata.Filelen)

	writeParallel(&wg, dwarfblk, ctxt, Segdwarf.Fileoff, Segdwarf.Vaddr, Segdwarf.Filelen)

	wg.Wait()
}

// Assembling the binary is broken into two steps:
//  - writing out the code/data/dwarf Segments
//  - writing out the architecture specific pieces.
// This function handles the second part.
func asmb2(ctxt *Link) bool {
	if ctxt.IsAIX() || ctxt.IsWasm() {
		return false
	}

	Symsize = 0
	Spsize = 0
	Lcsize = 0

	if ctxt.IsDarwin() {
		machlink := Domacholink(ctxt)
		if !*FlagS && ctxt.IsExternal() {
			symo := int64(Segdwarf.Fileoff + uint64(Rnd(int64(Segdwarf.Filelen), int64(*FlagRound))) + uint64(machlink))
			ctxt.Out.SeekSet(symo)
			Machoemitreloc(ctxt)
		}
		ctxt.Out.SeekSet(0)
		Asmbmacho(ctxt)
	}

	if ctxt.IsElf() {
		var symo int64
		if !*FlagS {
			symo = int64(Segdwarf.Fileoff + Segdwarf.Filelen)
			symo = Rnd(symo, int64(*FlagRound))
			ctxt.Out.SeekSet(symo)
			Asmelfsym(ctxt)
			ctxt.Out.Write(Elfstrdat)
			if ctxt.IsExternal() {
				Elfemitreloc(ctxt)
			}
		}
		ctxt.Out.SeekSet(0)
		Asmbelf(ctxt, symo)
	}

	if ctxt.IsWindows() {
		Asmbpe(ctxt)
	}

	if ctxt.IsPlan9() {
		if !*FlagS {
			*FlagS = true
			symo := int64(Segdata.Fileoff + Segdata.Filelen)
			ctxt.Out.SeekSet(symo)
			Asmplan9sym(ctxt)
		}
		ctxt.Out.SeekSet(0)
		WritePlan9Header(ctxt.Out, thearch.Plan9Magic, Entryvalue(ctxt), thearch.Plan9_64Bit)
	}

	if *FlagC {
		fmt.Printf("textsize=%d\n", Segtext.Filelen)
		fmt.Printf("datsize=%d\n", Segdata.Filelen)
		fmt.Printf("bsssize=%d\n", Segdata.Length-Segdata.Filelen)
		fmt.Printf("symsize=%d\n", Symsize)
		fmt.Printf("lcsize=%d\n", Lcsize)
		fmt.Printf("total=%d\n", Segtext.Filelen+Segdata.Length+uint64(Symsize)+uint64(Lcsize))
	}

	return true
}

// WritePlan9Header writes out the plan9 header at the present position in the OutBuf.
func WritePlan9Header(buf *OutBuf, magic uint32, entry int64, is64Bit bool) {
	if is64Bit {
		magic |= 0x00008000
	}
	buf.Write32b(magic)
	buf.Write32b(uint32(Segtext.Filelen))
	buf.Write32b(uint32(Segdata.Filelen))
	buf.Write32b(uint32(Segdata.Length - Segdata.Filelen))
	buf.Write32b(uint32(Symsize))
	if is64Bit {
		buf.Write32b(uint32(entry &^ 0x80000000))
	} else {
		buf.Write32b(uint32(entry))
	}
	buf.Write32b(uint32(Spsize))
	buf.Write32b(uint32(Lcsize))
	// amd64 includes the entry at the beginning of the symbol table.
	if is64Bit {
		buf.Write64b(uint64(entry))
	}
}