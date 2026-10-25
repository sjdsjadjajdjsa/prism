-- Prism's built-in Neovim setup. Prism draws the editor itself, so this only
-- sets behaviour, not looks beyond the colours Prism passes in.
local o = vim.o
o.number = true
o.mouse = 'a'
o.termguicolors = true
o.ignorecase = true
o.smartcase = true
o.expandtab = true
o.shiftwidth = 2
o.tabstop = 4
o.undofile = false
o.laststatus = 2
o.signcolumn = 'no'
o.scrolloff = 4
o.splitright = true
-- Swap files would land next to the user's code and show up in git.
o.swapfile = false
o.autoread = true

-- The AI edits files while they are open: pick its changes up within a second.
vim.fn.timer_start(1000, function()
  if vim.fn.mode() ~= 'c' then vim.cmd('silent! checktime') end
end, { ['repeat'] = -1 })
